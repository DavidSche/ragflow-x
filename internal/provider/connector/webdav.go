package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// WebDAVProvider lists files through a WebDAV server using PROPFIND Depth:1.
// Keys are paths relative to the DAV root. Scan state (directory stack and the
// last delivered child) is serialized into the opaque page token, so one page
// costs one PROPFIND per visited directory instead of a full-tree walk per page
// (doc/225 §8: listing budget per run).
type WebDAVProvider struct {
	propfindOnce sync.Once
	propfind     *http.Client

	downloadOnce sync.Once
	download     *http.Client
}

// propfindClient returns the provider's pooled PROPFIND client (created once
// per provider instance so a run's directory walk reuses keep-alive
// connections).
func (p *WebDAVProvider) propfindClient() *http.Client {
	p.propfindOnce.Do(func() { p.propfind = newConnectorHTTPClient(30 * time.Second) })
	return p.propfind
}

// downloadClient returns the provider's pooled download client.
func (p *WebDAVProvider) downloadClient() *http.Client {
	p.downloadOnce.Do(func() { p.download = newConnectorHTTPClient(60 * time.Second) })
	return p.download
}

// Type implements Provider.
func (*WebDAVProvider) Type() string { return model.ConnectorProviderWebDAV }

func webdavSpec(spec map[string]any) (baseURL, davPath string, err error) {
	baseURL, _ = spec["base_url"].(string)
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", "", fmt.Errorf("base_url is required")
	}
	base, perr := url.Parse(baseURL)
	if perr != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", "", fmt.Errorf("invalid base_url: %v", perr)
	}
	davPath, _ = spec["path"].(string)
	davPath = strings.TrimSpace(davPath)
	if davPath == "" {
		davPath = "/"
	}
	if !strings.HasPrefix(davPath, "/") {
		davPath = "/" + davPath
	}
	return baseURL, davPath, nil
}

func webdavCred(cred map[string]string) (username, password string, err error) {
	username = strings.TrimSpace(cred["username"])
	password = cred["password"]
	if username == "" || password == "" {
		return "", "", fmt.Errorf("username and password are required")
	}
	return username, password, nil
}

// ValidateSpec implements Provider.
func (*WebDAVProvider) ValidateSpec(spec map[string]any) error {
	_, _, err := webdavSpec(spec)
	return err
}

// Test implements Provider: a PROPFIND Depth:0 on the root.
func (p *WebDAVProvider) Test(ctx context.Context, spec map[string]any, cred map[string]string) error {
	baseURL, davPath, err := webdavSpec(spec)
	if err != nil {
		return err
	}
	username, password, err := webdavCred(cred)
	if err != nil {
		return err
	}
	resp, err := p.propfindRequest(ctx, baseURL, davPath, username, password, 0)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMultiStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("webdav probe failed (%s)", resp.Status)
	}
	return nil
}

type davDir struct {
	Abs string `json:"abs"`
	Rel string `json:"rel"`
}

type davCursor struct {
	Dir     *davDir  `json:"dir,omitempty"`
	After   string   `json:"after,omitempty"`
	Pending []davDir `json:"pending,omitempty"`
	Dirs    int      `json:"dirs,omitempty"`
}

func encodeDavCursor(c davCursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeDavCursor(token string) (davCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return davCursor{}, fmt.Errorf("invalid webdav cursor: %w", err)
	}
	var c davCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return davCursor{}, fmt.Errorf("invalid webdav cursor: %w", err)
	}
	return c, nil
}

// List implements Provider with a resumable depth-first scan.
func (p *WebDAVProvider) List(ctx context.Context, spec map[string]any, cred map[string]string, token string) ([]Entry, string, error) {
	baseURL, davPath, err := webdavSpec(spec)
	if err != nil {
		return nil, "", err
	}
	username, password, err := webdavCred(cred)
	if err != nil {
		return nil, "", err
	}
	cur := davCursor{Dir: &davDir{Abs: davPath, Rel: ""}}
	if token != "" {
		cur, err = decodeDavCursor(token)
		if err != nil {
			return nil, "", err
		}
	}
	entries := make([]Entry, 0, model.ConnectorMaxBatch)
	for {
		if cerr := ctx.Err(); cerr != nil {
			return nil, "", cerr
		}
		if cur.Dir == nil {
			if len(cur.Pending) == 0 {
				return entries, "", nil
			}
			next := cur.Pending[len(cur.Pending)-1]
			cur.Pending = cur.Pending[:len(cur.Pending)-1]
			cur.Dir = &next
			cur.After = ""
		}
		if cur.Dirs >= webdavMaxDirs {
			return nil, "", fmt.Errorf("webdav tree exceeds directory budget")
		}
		cur.Dirs++
		files, dirs, err := p.listDir(ctx, baseURL, *cur.Dir, username, password)
		if err != nil {
			return nil, "", err
		}
		for _, f := range files {
			if cur.After != "" && f.name <= cur.After {
				continue
			}
			entries = append(entries, f.entry)
			cur.After = f.name
			if len(entries) >= model.ConnectorMaxBatch {
				next, ierr := encodeDavCursor(cur)
				if ierr != nil {
					return nil, "", ierr
				}
				return entries, next, nil
			}
		}
		cur.Dir = nil
		cur.After = ""
		for i := len(dirs) - 1; i >= 0; i-- {
			cur.Pending = append(cur.Pending, dirs[i])
		}
	}
}

type davFile struct {
	name  string
	entry Entry
}

// listDir lists one collection, returning direct file children sorted by name
// and direct collections sorted by name.
func (p *WebDAVProvider) listDir(ctx context.Context, baseURL string, dir davDir, username, password string) ([]davFile, []davDir, error) {
	resp, err := p.propfindRequest(ctx, baseURL, dir.Abs, username, password, 1)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusMultiStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("webdav propfind %s failed (%s)", dir.Abs, resp.Status)
	}
	result, perr := decodeMultiStatus(resp.Body)
	_ = resp.Body.Close()
	if perr != nil {
		return nil, nil, perr
	}

	baseAbs := strings.TrimSuffix(dir.Abs, "/")
	if dir.Rel == "" {
		// Trust the server's canonical self href for the root so servers that
		// publish a different absolute path still yield correct keys.
		for _, r := range result.Responses {
			if h := strings.TrimSuffix(decodeHref(r.Href), "/"); h != "" {
				baseAbs = h
				break
			}
		}
	}

	files := make([]davFile, 0, len(result.Responses))
	dirs := make([]davDir, 0, 4)
	for _, r := range result.Responses {
		href := strings.TrimSuffix(decodeHref(r.Href), "/")
		if href == "" || href == baseAbs {
			continue
		}
		child, ok := davChild(href, baseAbs)
		if !ok {
			continue
		}
		rel := path.Join(dir.Rel, child)
		if isCollection(r) {
			dirs = append(dirs, davDir{Abs: baseAbs + "/" + child, Rel: rel})
			continue
		}
		files = append(files, davFile{name: child, entry: webdavEntryOf(rel, r)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Rel < dirs[j].Rel })
	return files, dirs, nil
}

// davChild returns the direct child name of href under baseAbs.
func davChild(href, baseAbs string) (string, bool) {
	prefix := baseAbs + "/"
	if baseAbs == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(href, prefix) {
		return "", false
	}
	child := strings.TrimPrefix(href, prefix)
	if child == "" || strings.Contains(child, "/") {
		return "", false
	}
	return child, true
}

func isCollection(r webdavResponse) bool {
	for _, ps := range r.Propstats {
		if ps.Prop.Resourcetype.Collection {
			return true
		}
	}
	return false
}

// Download implements Provider: HTTP GET with basic auth.
func (p *WebDAVProvider) Download(ctx context.Context, spec map[string]any, cred map[string]string, key string) ([]byte, error) {
	baseURL, davPath, err := webdavSpec(spec)
	if err != nil {
		return nil, err
	}
	username, password, err := webdavCred(cred)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	target, err := davURL(baseURL, path.Join(davPath, key))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(username, password)
	resp, err := p.downloadClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webdav download %s failed (%s)", key, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxObjectBytes {
		return nil, fmt.Errorf("object exceeds the 25 MiB connector limit")
	}
	return data, nil
}

func webdavEntryOf(rel string, r webdavResponse) Entry {
	e := Entry{Key: rel, Kind: "file"}
	for _, ps := range r.Propstats {
		p := ps.Prop
		if p.GetContentLength != nil {
			e.Size = *p.GetContentLength
		}
		if p.GetETag != nil {
			e.Revision = strings.Trim(*p.GetETag, `"`)
		}
		if p.GetLastModified != nil {
			if t, err := http.ParseTime(*p.GetLastModified); err == nil {
				e.MTime = t.UTC()
			}
		}
	}
	if e.Revision == "" {
		e.Revision = fmt.Sprintf("%d-%d", e.MTime.Unix(), e.Size)
	}
	return e
}

func decodeHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if u, err := url.Parse(href); err == nil && u.Path != "" {
		return u.Path
	}
	return href
}

// davURL joins the DAV base URL (which may itself carry a path prefix) with an
// absolute DAV path.
func davURL(baseURL, davPath string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(davPath, "/")
	base.RawPath = ""
	return base.String(), nil
}

func (p *WebDAVProvider) propfindRequest(ctx context.Context, baseURL, davPath, username, password string, depth int) (*http.Response, error) {
	target, err := davURL(baseURL, davPath)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", target, strings.NewReader(
		`<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop>`+
			`<D:resourcetype/><D:getetag/><D:getcontentlength/><D:getlastmodified/>`+
			`</D:prop></D:propfind>`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", fmt.Sprintf("%d", depth))
	req.Header.Set("Content-Type", "application/xml")
	req.SetBasicAuth(username, password)
	return p.propfindClient().Do(req)
}

// webdav XML shapes (namespace-local tags so any DAV: prefix matches).

type webdavMultiStatus struct {
	Responses []webdavResponse `xml:"response"`
}

type webdavResponse struct {
	Href      string           `xml:"href"`
	Propstats []webdavPropstat `xml:"propstat"`
}

type webdavPropstat struct {
	Prop struct {
		Resourcetype struct {
			Collection bool `xml:"collection"`
		} `xml:"resourcetype"`
		GetETag          *string `xml:"getetag"`
		GetLastModified  *string `xml:"getlastmodified"`
		GetContentLength *int64  `xml:"getcontentlength"`
	} `xml:"prop"`
}

func decodeMultiStatus(r io.Reader) (*webdavMultiStatus, error) {
	var result webdavMultiStatus
	if err := xml.NewDecoder(r).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse webdav multistatus: %w", err)
	}
	return &result, nil
}

const webdavMaxDirs = 20000
