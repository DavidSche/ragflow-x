package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

// HTTPClient talks to a real RAGFlow instance over its HTTP API (/api/v1).
type HTTPClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewHTTPClient builds a client with a default LoggingMiddleware.
// Signature unchanged for backward compatibility.
func NewHTTPClient(baseURL, apiKey string, timeout time.Duration, maxConns int) *HTTPClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxConns <= 0 {
		maxConns = 20
	}
	transport := chainRoundTrippers(
		&http.Transport{
			MaxIdleConns:    maxConns,
			MaxConnsPerHost: maxConns,
			DialContext:     pinnedDialer{}.DialContext,
		},
		LoggingMiddleware("/providers/"),
	)
	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:       timeout,
			Transport:     transport,
			CheckRedirect: providerRedirectPolicy,
		},
	}
}

type pinnedDialer struct{}

func (pinnedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("provider address is invalid")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("provider host cannot be resolved")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func providerRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("stopped after 3 redirects")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return errors.New("provider redirects must remain on the configured host")
	}
	return nil
}

// NewHTTPClientWithMiddleware builds a client with an explicit middleware chain.
// Unlike NewHTTPClient it does NOT attach a default LoggingMiddleware: callers
// must pass one explicitly if request/response logging is required. Sensitive
// request bodies (e.g. /providers/<name>/connection carrying an api_key) are
// redacted by LoggingMiddleware regardless of how many middlewares are stacked.
func NewHTTPClientWithMiddleware(baseURL, apiKey string, timeout time.Duration,
	maxConns int, middlewares ...RoundTripperMiddleware) *HTTPClient {

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxConns <= 0 {
		maxConns = 20
	}
	baseTransport := &http.Transport{
		MaxIdleConns:    maxConns,
		MaxConnsPerHost: maxConns,
		DialContext:     pinnedDialer{}.DialContext,
	}
	transport := chainRoundTrippers(baseTransport, middlewares...)

	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:       timeout,
			Transport:     transport,
			CheckRedirect: providerRedirectPolicy,
		},
	}
}

// Name implements the Client interface.
func (c *HTTPClient) Name() string { return "http" }

// EngineVersion reuses the version probe's HTTP contract.
func (c *HTTPClient) EngineVersion(ctx context.Context) (string, error) {
	return NewVersionProbe(c).ProbeVersion(ctx)
}

// modelVerifySuccessLabel mirrors RAGFlow's ModelVerifyStatusEnum success value.
const modelVerifySuccessLabel = "success"

type envelope struct {
	Code    int             `json:"code"`
	Message interface{}     `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// buildRequest constructs an authenticated HTTP request with GetBody for retry replay.
func (c *HTTPClient) buildRequest(ctx context.Context, method, path string,
	body io.Reader, contentType string) (*http.Request, error) {

	u, err := url.Parse(c.baseURL + "/api/v1" + path)
	if err != nil {
		return nil, wrapError("parse ragflow url", err)
	}

	var bodyBytes []byte
	var contentLength int64
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
		contentLength = int64(len(bodyBytes))
		body = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.apiKey)
	if requestID := requestid.FromContext(ctx); requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if bodyBytes != nil {
		req.ContentLength = contentLength
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	return req, nil
}

// doAndParse executes a request and parses the RAGFlow envelope into out.
func (c *HTTPClient) doAndParse(ctx context.Context, method, path string,
	body io.Reader, contentType string, out interface{}) error {

	req, err := c.buildRequest(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Pass through structured errors (e.g. from RetryMiddleware) without wrapping.
		var ragErr *Error
		if errors.As(err, &ragErr) {
			return ragErr
		}
		return transportError(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return wrapError("read ragflow response", err)
	}

	if resp.StatusCode >= 300 {
		return NewErrorFromHTTPResponse(resp, raw)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return wrapError("decode ragflow response", err)
	}
	if env.Code != 0 {
		return NewErrorFromResponse(resp.StatusCode, raw)
	}

	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return &Error{
				Type:    ErrorTypeProtocol,
				Message: "failed to decode response data",
				Cause:   err,
			}
		}
	}
	return nil
}

// do is the legacy entry point that delegates to doAndParse.
func (c *HTTPClient) do(ctx context.Context, method, path string, body io.Reader, contentType string, out interface{}) error {
	return c.doAndParse(ctx, method, path, body, contentType, out)
}

// doRawRequest executes a request without envelope parsing (for binary downloads).
func (c *HTTPClient) doRawRequest(ctx context.Context, method, path string,
	body io.Reader, contentType string) (*http.Response, error) {

	req, err := c.buildRequest(ctx, method, path, body, contentType)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	return resp, nil
}

func transportError(err error) *Error {
	if errors.Is(err, context.Canceled) {
		return &Error{Type: ErrorTypeCancelled, Message: "request canceled", Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Type: ErrorTypeTimeout, Message: "request deadline exceeded", Cause: err}
	}
	return wrapError("request ragflow", err)
}

func (c *HTTPClient) CreateDataset(ctx context.Context, req CreateDatasetRequest) (*Dataset, error) {
	b, _ := json.Marshal(req)
	var out Dataset
	if err := c.do(ctx, http.MethodPost, "/datasets", bytes.NewReader(b), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListDatasets(ctx context.Context) ([]Dataset, error) {
	const pageSize = 100
	var all []Dataset
	for page := 1; ; page++ {
		var out []Dataset
		path := fmt.Sprintf("/datasets?page=%d&page_size=%d", page, pageSize)
		if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < pageSize {
			break
		}
	}
	return all, nil
}

func (c *HTTPClient) DeleteDataset(ctx context.Context, id string) error {
	body, err := json.Marshal(map[string][]string{"ids": {id}})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/datasets", bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) CreateDocument(ctx context.Context, datasetID string, doc *DocumentUpload) (*Document, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fw, err := writer.CreateFormFile("file", doc.Name)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(doc.Content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	path := "/datasets/" + url.PathEscape(datasetID) + "/documents"
	// RAGFlow returns an array of the uploaded document on success; the first
	// entry is the created document with its real id and processing status.
	var out []Document
	if err := c.do(ctx, http.MethodPost, path, &buf, writer.FormDataContentType(), &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ragflow returned no document after upload")
	}
	return &out[0], nil
}

// UploadChatFile uploads a temporary chat attachment blob and returns its
// RAGFlow file metadata so it can be attached to a chat messages.files field.
func (c *HTTPClient) UploadChatFile(ctx context.Context, filename string, content []byte) (*UploadedFile, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fw, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	var out UploadedFile
	if err := c.do(ctx, http.MethodPost, "/documents/upload", &buf, writer.FormDataContentType(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadAgentFile uploads a temporary Agent attachment and returns RAGFlow's
// uploaded-file metadata for the request body's top-level `files` field.
func (c *HTTPClient) UploadAgentFile(ctx context.Context, agentID, filename string, content []byte) (*UploadedFile, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fw, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	var out UploadedFile
	path := "/agents/" + url.PathEscape(agentID) + "/upload"
	if err := c.do(ctx, http.MethodPost, path, &buf, writer.FormDataContentType(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListDocuments(ctx context.Context, datasetID string) ([]Document, error) {
	const pageSize = 100
	var all []Document
	for page := 1; ; page++ {
		path := fmt.Sprintf(
			"/datasets/%s/documents?page=%d&page_size=%d",
			url.PathEscape(datasetID), page, pageSize,
		)
		var out struct {
			Docs  []Document `json:"docs"`
			Total int64      `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
			return nil, err
		}
		all = append(all, out.Docs...)
		if (out.Total > 0 && int64(len(all)) >= out.Total) || (out.Total <= 0 && len(out.Docs) < pageSize) {
			break
		}
	}
	return all, nil
}

func (c *HTTPClient) ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	ids, _ := json.Marshal(documentIDs)
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/parse"
	payload := fmt.Sprintf(`{"document_ids":%s}`, ids)
	return c.do(ctx, http.MethodPost, path, bytes.NewReader([]byte(payload)), "application/json", nil)
}

func (c *HTTPClient) StopDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	ids, _ := json.Marshal(documentIDs)
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/stop"
	payload := fmt.Sprintf(`{"document_ids":%s}`, ids)
	return c.do(ctx, http.MethodPost, path, bytes.NewReader([]byte(payload)), "application/json", nil)
}

func (c *HTTPClient) DeleteDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	ids, _ := json.Marshal(map[string]interface{}{"ids": documentIDs})
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents"
	return c.do(ctx, http.MethodDelete, path, bytes.NewReader(ids), "application/json", nil)
}

func (c *HTTPClient) SetDocumentsStatus(ctx context.Context, datasetID string, documentIDs []string, enabled bool) error {
	status := "0"
	if enabled {
		status = "1"
	}
	body, _ := json.Marshal(map[string]interface{}{"doc_ids": documentIDs, "status": status})
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/batch-update-status"
	return c.do(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) UpdateDocumentMetadata(ctx context.Context, datasetID, documentID string, metadata map[string]interface{}) error {
	body, _ := json.Marshal(map[string]interface{}{"metadata": metadata})
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(documentID) + "/metadata/config"
	return c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) ListDocumentChunks(ctx context.Context, datasetID, documentID string, page, pageSize int) ([]Chunk, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	} else if pageSize > 100 {
		pageSize = 100
	}
	path := fmt.Sprintf("/datasets/%s/documents/%s/chunks?page=%d&page_size=%d", url.PathEscape(datasetID), url.PathEscape(documentID), page, pageSize)
	var out struct {
		Chunks []Chunk `json:"chunks"`
		Total  int64   `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, 0, err
	}
	return out.Chunks, out.Total, nil
}

func (c *HTTPClient) GetDatasetConfig(ctx context.Context, datasetID string) (*DatasetConfig, error) {
	var out DatasetConfig
	if err := c.do(ctx, http.MethodGet, "/datasets/"+url.PathEscape(datasetID), nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) UpdateDatasetConfig(ctx context.Context, datasetID string, cfg DatasetConfigUpdate) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal dataset config: %w", err)
	}
	path := "/datasets/" + url.PathEscape(datasetID)
	// RAGFlow can transiently fail concurrent writes (e.g. SQLite DB locks while a
	// parse task commits progress). Retry idempotent config replacements with backoff.
	const attempts = 4
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 300 * time.Millisecond)
		}
		err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", nil)
		if err == nil {
			return nil
		}
		if !isTransientRAGFlowErr(err) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("update dataset config failed after retries: %w", lastErr)
}

// isTransientRAGFlowErr reports whether a RAGFlow error is likely a transient DB
// lock / contention that a retry can clear, versus a persistent request error.
func isTransientRAGFlowErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status 500") ||
		strings.Contains(msg, "lock") ||
		strings.Contains(msg, "busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "serialization")
}

func (c *HTTPClient) GetDocumentContent(ctx context.Context, documentID string) ([]byte, string, error) {
	resp, err := c.doRawRequest(ctx, http.MethodGet,
		"/documents/"+url.PathEscape(documentID)+"/preview", nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", NewErrorFromHTTPResponse(resp, b)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", wrapError("read ragflow response", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (c *HTTPClient) DeleteChunks(ctx context.Context, datasetID, documentID string, chunkIDs []string) error {
	body, _ := json.Marshal(map[string]interface{}{"chunk_ids": chunkIDs})
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(documentID) + "/chunks"
	return c.do(ctx, http.MethodDelete, path, bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) SetChunksAvailable(ctx context.Context, datasetID, documentID string, chunkIDs []string, enabled bool) error {
	avail := 0
	if enabled {
		avail = 1
	}
	body, _ := json.Marshal(map[string]interface{}{"chunk_ids": chunkIDs, "available_int": avail})
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(documentID) + "/chunks"
	return c.do(ctx, http.MethodPatch, path, bytes.NewReader(body), "application/json", nil)
}
func (c *HTTPClient) Health(ctx context.Context) (*Health, error) {
	resp, err := c.doRawRequest(ctx, http.MethodGet, "/system/healthz", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError("read ragflow health", err)
	}
	var out Health
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}
	if resp.StatusCode >= 300 {
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}
	return &out, nil
}

// UpsertModelProvider adds the factory (if missing), then creates/updates a
// provider instance carrying the requested model.
func (c *HTTPClient) UpsertModelProvider(ctx context.Context, req RegisterModelProviderRequest) error {
	provBody, _ := json.Marshal(map[string]string{"provider_name": req.FactoryName})
	if err := c.do(ctx, http.MethodPut, "/providers", bytes.NewReader(provBody), "application/json", nil); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}

	modelTypes := req.ModelTypes
	if len(modelTypes) == 0 {
		modelTypes = []string{"chat"}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body := map[string]interface{}{
		"instance_name": req.InstanceName,
		"api_key":       req.APIKey,
		"base_url":      req.BaseURL,
		"region":        "",
		"model_info": []interface{}{
			map[string]interface{}{
				"model_type": modelTypes,
				"model_name": req.ModelName,
				"max_tokens": maxTokens,
				"extra":      map[string]interface{}{"is_tools": req.IsTools, "thinking": req.Thinking},
			},
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := "/providers/" + url.PathEscape(req.FactoryName) + "/instances"
	return c.do(ctx, http.MethodPost, path, bytes.NewReader(b), "application/json", nil)
}

func (c *HTTPClient) ListProviders(ctx context.Context, available bool) ([]ProviderInfo, error) {
	path := "/providers"
	if available {
		path += "?available=true"
	}
	var out []ProviderInfo
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) AddProvider(ctx context.Context, providerName string) error {
	b, _ := json.Marshal(map[string]string{"provider_name": providerName})
	if err := c.do(ctx, http.MethodPut, "/providers", bytes.NewReader(b), "application/json", nil); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}

func (c *HTTPClient) DeleteProvider(ctx context.Context, providerName string) error {
	return c.do(ctx, http.MethodDelete, "/providers/"+url.PathEscape(providerName), nil, "", nil)
}

func (c *HTTPClient) ListProviderModels(ctx context.Context, providerName, apiKey, baseURL string) ([]ProviderModel, error) {
	path := "/providers/" + url.PathEscape(providerName) + "/models"
	q := url.Values{}
	if apiKey != "" {
		q.Set("api_key", apiKey)
	}
	if baseURL != "" {
		q.Set("base_url", baseURL)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []ProviderModel
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) ListProviderInstances(ctx context.Context, providerName string) ([]ProviderInstance, error) {
	path := "/providers/" + url.PathEscape(providerName) + "/instances"
	var out []ProviderInstance
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func instanceModelInfo(m []ModelInfo) []interface{} {
	if len(m) == 0 {
		return nil
	}
	modelTypes := []string{"chat"}
	out := make([]interface{}, 0, len(m))
	for _, mi := range m {
		mt := mi.ModelType
		if len(mt) == 0 {
			mt = modelTypes
		}
		maxTokens := mi.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 8192
		}
		extra := mi.Extra
		if extra == nil {
			extra = map[string]interface{}{}
		}
		out = append(out, map[string]interface{}{
			"model_type": mt,
			"model_name": mi.ModelName,
			"max_tokens": maxTokens,
			"extra":      extra,
		})
	}
	return out
}

func (c *HTTPClient) CreateProviderInstance(ctx context.Context, req RegisterModelProviderRequest) (*ProviderInstance, error) {
	body := map[string]interface{}{
		"instance_name": req.InstanceName,
		"api_key":       req.APIKey,
		"base_url":      req.BaseURL,
		"region":        req.Region,
	}
	if info := instanceModelInfo(req.Models); info != nil {
		body["model_info"] = info
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	path := "/providers/" + url.PathEscape(req.FactoryName) + "/instances"
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(b), "application/json", nil); err != nil {
		return nil, err
	}
	instances, err := c.ListProviderInstances(ctx, req.FactoryName)
	if err != nil {
		return &ProviderInstance{InstanceName: req.InstanceName}, nil
	}
	for index := range instances {
		if strings.EqualFold(strings.TrimSpace(instances[index].InstanceName), strings.TrimSpace(req.InstanceName)) {
			return &instances[index], nil
		}
	}
	return &ProviderInstance{InstanceName: req.InstanceName}, nil
}

func (c *HTTPClient) UpdateProviderInstance(ctx context.Context, providerName, instanceName string, req RegisterModelProviderRequest) error {
	body := map[string]interface{}{
		"instance_name": req.InstanceName,
		"api_key":       req.APIKey,
		"base_url":      req.BaseURL,
		"region":        req.Region,
		"verify":        true,
	}
	body["model_info"] = instanceModelInfo(req.Models)
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName)
	return c.do(ctx, http.MethodPut, path, bytes.NewReader(b), "application/json", nil)
}

func (c *HTTPClient) DeleteProviderInstances(ctx context.Context, providerName string, instanceIDs []string) error {
	identifiers := append([]string(nil), instanceIDs...)
	if instances, err := c.ListProviderInstances(ctx, providerName); err == nil {
		identifiers = make([]string, 0, len(instanceIDs))
		for _, identifier := range instanceIDs {
			resolved := identifier
			for _, instance := range instances {
				if instance.ID == identifier || strings.EqualFold(strings.TrimSpace(instance.InstanceName), strings.TrimSpace(identifier)) {
					resolved = instance.ID
					break
				}
			}
			if resolved != "" {
				identifiers = append(identifiers, resolved)
			}
		}
	}
	body, _ := json.Marshal(map[string]interface{}{"instances": identifiers})
	path := "/providers/" + url.PathEscape(providerName) + "/instances"
	return c.do(ctx, http.MethodDelete, path, bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) ListInstanceModels(ctx context.Context, providerName, instanceName string, supported bool) ([]ProviderModel, error) {
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName) + "/models"
	if supported {
		path += "?supported=true"
	}
	var out []ProviderModel
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) AddModelToInstance(ctx context.Context, providerName, instanceName string, model ModelInfo) error {
	modelTypes := model.ModelType
	if len(modelTypes) == 0 {
		modelTypes = []string{"chat"}
	}
	maxTokens := model.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body := map[string]interface{}{
		"model_name": model.ModelName,
		"model_type": modelTypes,
		"max_tokens": maxTokens,
	}
	if model.Extra != nil {
		body["extra"] = model.Extra
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName) + "/models"
	return c.do(ctx, http.MethodPost, path, bytes.NewReader(b), "application/json", nil)
}

func (c *HTTPClient) UpdateModel(ctx context.Context, providerName, instanceName, modelName string, update ModelUpdate) error {
	body := map[string]interface{}{}
	if update.Status != "" {
		body["status"] = update.Status
	}
	if update.MaxTokens > 0 {
		body["max_tokens"] = update.MaxTokens
	}
	if update.ModelType != nil {
		body["model_type"] = update.ModelType
	}
	if update.Extra != nil {
		body["extra"] = update.Extra
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName) + "/models/" + url.PathEscape(modelName)
	return c.do(ctx, http.MethodPatch, path, bytes.NewReader(b), "application/json", nil)
}

func (c *HTTPClient) DeleteModelsFromInstance(ctx context.Context, providerName, instanceName string, modelNames []string) error {
	body, _ := json.Marshal(map[string]interface{}{"model_name": modelNames})
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName) + "/models"
	return c.do(ctx, http.MethodDelete, path, bytes.NewReader(body), "application/json", nil)
}

func (c *HTTPClient) VerifyConnection(ctx context.Context, providerName, apiKey, baseURL, region string, modelInfo []ModelInfo) (map[string]string, error) {
	body := map[string]interface{}{
		"api_key":  apiKey,
		"base_url": baseURL,
		"region":   region,
	}
	if info := instanceModelInfo(modelInfo); info != nil {
		body["model_info"] = info
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	path := "/providers/" + url.PathEscape(providerName) + "/connection"
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(b), "application/json", nil); err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, mi := range modelInfo {
		result[mi.ModelName] = modelVerifySuccessLabel
	}
	return result, nil
}

func (c *HTTPClient) ChatToModel(ctx context.Context, providerName, instanceName, modelName, message string, stream, thinking bool) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{"message": message, "stream": stream, "thinking": thinking})
	path := "/providers/" + url.PathEscape(providerName) + "/instances/" + url.PathEscape(instanceName) + "/models/" + url.PathEscape(modelName)
	var out json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return "", err
	}
	return string(out), nil
}

// truncateResponseLog caps a response body for logs so large list replies or
// chat payloads do not flood the logs.
func truncateResponseLog(b []byte) string {
	const max = 4096
	if len(b) > max {
		return fmt.Sprintf("%s…(%d bytes total)", string(b[:max]), len(b))
	}
	return string(b)
}

// GetChunkImage fetches a referenced chunk's image bytes from RAGFlow.
func (c *HTTPClient) GetChunkImage(ctx context.Context, imageID string) ([]byte, string, error) {
	resp, err := c.doRawRequest(ctx, http.MethodGet,
		"/documents/images/"+url.PathEscape(imageID), nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", NewErrorFromHTTPResponse(resp, b)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", wrapError("read ragflow response", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
