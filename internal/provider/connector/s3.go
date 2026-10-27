package connector

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// S3Provider lists objects from any S3-compatible endpoint (AWS, MinIO, R2).
// Credentials: spec.access_key_id + spec.access_secret (the latter lives in
// the sealed credential map). Requests are signed with a hand-rolled AWS SigV4
// (no SDK dependency) and always HTTPS unless the endpoint is localhost.
type S3Provider struct {
	listOnce sync.Once
	list     *http.Client

	downloadOnce sync.Once
	download     *http.Client
}

// listClient returns the provider's pooled list client (created once per
// provider instance so a run's pages reuse keep-alive connections).
func (p *S3Provider) listClient() *http.Client {
	p.listOnce.Do(func() { p.list = newConnectorHTTPClient(30 * time.Second) })
	return p.list
}

// downloadClient returns the provider's pooled download client.
func (p *S3Provider) downloadClient() *http.Client {
	p.downloadOnce.Do(func() { p.download = newConnectorHTTPClient(60 * time.Second) })
	return p.download
}

// Type implements Provider.
func (*S3Provider) Type() string { return model.ConnectorProviderS3 }

func s3Spec(spec map[string]any) (endpoint, region, bucket, prefix string, err error) {
	endpoint, _ = spec["endpoint"].(string)
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return "", "", "", "", fmt.Errorf("endpoint is required")
	}
	region, _ = spec["region"].(string)
	if region == "" {
		region = "us-east-1"
	}
	bucket, _ = spec["bucket"].(string)
	if bucket == "" {
		return "", "", "", "", fmt.Errorf("bucket is required")
	}
	prefix, _ = spec["prefix"].(string)
	prefix = strings.Trim(prefix, "/")
	return endpoint, region, bucket, prefix, nil
}

// ValidateSpec implements Provider.
func (*S3Provider) ValidateSpec(spec map[string]any) error {
	_, _, _, _, err := s3Spec(spec)
	return err
}

func s3Cred(cred map[string]string) (accessKey, secretKey string, err error) {
	accessKey = strings.TrimSpace(cred["access_key_id"])
	secretKey = strings.TrimSpace(cred["access_secret"])
	// Accept the doc/225 §7.2 example spelling (access_key/secret_key) as an
	// alias so API clients are not coupled to the internal field names.
	if accessKey == "" {
		accessKey = strings.TrimSpace(cred["access_key"])
	}
	if secretKey == "" {
		secretKey = strings.TrimSpace(cred["secret_key"])
	}
	if accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("access_key_id/access_key and access_secret/secret_key are required")
	}
	return accessKey, secretKey, nil
}

// Test implements Provider: a 1-object ListObjectsV2 probe proves endpoint +
// credentials + bucket reachability.
func (p *S3Provider) Test(ctx context.Context, spec map[string]any, cred map[string]string) error {
	accessKey, secretKey, err := s3Cred(cred)
	if err == nil {
		_, _, _, _, err = s3Spec(spec)
	}
	if err != nil {
		return err
	}
	resp, err := p.s3GetPage(ctx, spec, accessKey, secretKey, "", 1)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return s3StatusError(resp)
	}
	return nil
}

type s3ListBucketResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
}

// List implements Provider: ListObjectsV2 pagination via the server's opaque
// continuation token (@max-keys = ConnectorMaxBatch per round trip).
func (p *S3Provider) List(ctx context.Context, spec map[string]any, cred map[string]string, token string) ([]Entry, string, error) {
	accessKey, secretKey, err := s3Cred(cred)
	if err == nil {
		_, _, _, _, err = s3Spec(spec)
	}
	if err != nil {
		return nil, "", err
	}
	resp, err := p.s3GetPage(ctx, spec, accessKey, secretKey, token, model.ConnectorMaxBatch)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", s3StatusError(resp)
	}

	var result s3ListBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("parse s3 list response: %w", err)
	}
	entries := make([]Entry, 0, len(result.Contents))
	for _, it := range result.Contents {
		// Directory placeholders (key ending in '/', zero bytes) are not
		// documents; skipping them keeps the ledger free of empty entries.
		if strings.HasSuffix(it.Key, "/") || it.Key == "" {
			continue
		}
		mtime, _ := time.Parse(time.RFC3339, it.LastModified)
		rev := strings.Trim(it.ETag, `"`)
		if rev == "" {
			rev = fmt.Sprintf("%d-%d", mtime.Unix(), it.Size)
		}
		entries = append(entries, Entry{
			Key:      it.Key,
			Size:     it.Size,
			MTime:    mtime.UTC(),
			Revision: rev,
			Kind:     "file",
		})
	}
	next := ""
	if result.IsTruncated {
		next = result.NextContinuationToken
	}
	return entries, next, nil
}

// Download implements Provider: GET /bucket/key (SigV4-signed).
func (p *S3Provider) Download(ctx context.Context, spec map[string]any, cred map[string]string, key string) ([]byte, error) {
	accessKey, secretKey, err := s3Cred(cred)
	if err == nil {
		_, _, _, _, err = s3Spec(spec)
	}
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	endpoint, region, bucket, _, err := s3Spec(spec)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	u.Path = "/" + bucket + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	if err := s3Sign(req, accessKey, secretKey, region, "s3"); err != nil {
		return nil, err
	}
	resp, err := p.downloadClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, s3StatusError(resp)
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

func (p *S3Provider) s3GetPage(ctx context.Context, spec map[string]any, accessKey, secretKey, continuationToken string, maxKeys int) (*http.Response, error) {
	endpoint, region, bucket, prefix, err := s3Spec(spec)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("list-type", "2")
	if prefix != "" {
		query.Set("prefix", prefix)
	}
	if maxKeys > 0 {
		query.Set("max-keys", fmt.Sprintf("%d", maxKeys))
	}
	if continuationToken != "" {
		query.Set("continuation-token", continuationToken)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	u.Path = "/" + bucket
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	if err := s3Sign(req, accessKey, secretKey, region, "s3"); err != nil {
		return nil, err
	}
	return p.listClient().Do(req)
}

func s3StatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("s3 request failed (%s): %s", resp.Status, msg)
}

// s3Sign applies an AWS Signature Version 4 Authorization header for S3
// (service "s3"). HTTPS is enforced except for loopback endpoints so test
// harnesses and MinIO-on-localhost keep working.
func s3Sign(req *http.Request, accessKey, secretKey, region, service string) error {
	u := req.URL
	if u.Scheme != "https" && !strings.HasPrefix(u.Host, "localhost") && !strings.HasPrefix(u.Host, "127.") {
		return fmt.Errorf("s3 endpoint must be https unless localhost: %s", u.Host)
	}
	host := u.Host
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("x-amz-date", amzDate)

	canonicalURI := u.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := encodeCanonicalQuery(u.RawQuery)
	headerNames := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	var headers strings.Builder
	for _, hn := range headerNames {
		val := req.Header.Get(hn)
		if hn == "host" {
			val = host
		}
		headers.WriteString(hn + ":" + strings.TrimSpace(val) + "\n")
	}
	signedHeaders := strings.Join(headerNames, ";")
	payloadHash := req.Header.Get("x-amz-content-sha256")
	if payloadHash == "" {
		payloadHash = sha256Hex(nil)
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		headers.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+accessKey+"/"+credentialScope+
			", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

// encodeCanonicalQuery renders rawQuery in the SigV4 canonical form:
// parameters sorted by name, then by value, each strictly URI-encoded
// (awsURIEncode). Sorting "k=v" strings as a whole is only equivalent for
// single-valued parameters; multi-valued parameters require the two-level
// order or the signature breaks.
func encodeCanonicalQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, _ := url.ParseQuery(rawQuery)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(values))
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsURIEncode(k)+"="+awsURIEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func awsURIEncode(s string) string {
	const upperHex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0x0f])
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
