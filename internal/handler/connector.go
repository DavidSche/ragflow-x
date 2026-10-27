package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// connectorPayloadFromRequest builds the approval/creation payload surface.
type connectorPayloadFromRequest struct {
	Name                string         `json:"name"`
	Provider            string         `json:"provider"`
	ProviderType        string         `json:"provider_type"` // doc/225 §7.2 spelling
	AdapterKind         string         `json:"adapter_kind"`
	ExternalConnectorID string         `json:"external_connector_id"`
	TargetDataset       string         `json:"target_dataset_id"`
	Spec                map[string]any `json:"spec"`
	Credential          map[string]any `json:"credential"`
	// Schedule accepts either the flat form ("schedule":"interval") or the
	// doc/225 §7.2 object form ({"kind":"interval","minutes":30}).
	Schedule      any   `json:"schedule"`
	ScheduleValue int   `json:"schedule_value"`
	TZOffsetMin   *int  `json:"schedule_tz_offset_min"`
	Sensitive     *bool `json:"sensitive"`
	Enabled       *bool `json:"enabled"`
}

func (p connectorPayloadFromRequest) provider() string {
	if strings.TrimSpace(p.Provider) != "" {
		return p.Provider
	}
	return p.ProviderType
}

func (p connectorPayloadFromRequest) schedule() (string, int) {
	kind, value := normalizeConnectorScheduleInput(p.Schedule, p.ScheduleValue)
	// doc/225 F7: the object form carries a wall-clock HHMM; store UTC
	// minute-of-day. Flat schedule_value is already UTC (legacy/API form).
	if kind == model.ConnectorScheduleDaily {
		if _, ok := p.Schedule.(map[string]any); ok {
			value = connectorLocalMinuteToUTC(value, p.tzOffset())
		}
	}
	return kind, value
}

// tzOffset returns the display offset; daily object payloads default to +08:00
// (doc/225 F7) when the client omits it.
func (p connectorPayloadFromRequest) tzOffset() int {
	if p.TZOffsetMin == nil {
		if p.Schedule != nil {
			return model.ConnectorDefaultTZOffsetMin
		}
		return 0
	}
	return *p.TZOffsetMin
}

func connectorLocalMinuteToUTC(value, offset int) int {
	offset %= 1440
	return ((value-offset)%1440 + 1440) % 1440
}

func (p connectorPayloadFromRequest) toMap() map[string]any {
	kind, value := p.schedule()
	out := map[string]any{
		"name":                   p.Name,
		"provider":               p.provider(),
		"adapter_kind":           strings.TrimSpace(p.AdapterKind),
		"external_connector_id":  strings.TrimSpace(p.ExternalConnectorID),
		"target_dataset_id":      strings.TrimSpace(p.TargetDataset),
		"spec":                   p.Spec,
		"credential":             p.Credential,
		"schedule":               kind,
		"schedule_value":         value,
		"schedule_tz_offset_min": p.tzOffset(),
	}
	if p.Sensitive != nil {
		out["sensitive"] = *p.Sensitive
	}
	if p.Enabled != nil {
		out["enabled"] = *p.Enabled
	}
	return out
}

// normalizeConnectorScheduleInput accepts the string kind ("interval") and the
// documented object form ({"kind":"daily","hhmm":"09:30"}).
func normalizeConnectorScheduleInput(raw any, fallbackValue int) (string, int) {
	switch v := raw.(type) {
	case nil:
		return "", fallbackValue
	case string:
		return strings.TrimSpace(v), fallbackValue
	case map[string]any:
		kind, _ := v["kind"].(string)
		kind = strings.TrimSpace(kind)
		if n, ok := numericScheduleValue(v["minutes"]); ok {
			return kind, n
		}
		if n, ok := numericScheduleValue(v["value"]); ok {
			return kind, n
		}
		if hhmm, ok := v["hhmm"].(string); ok {
			if n, ok := parseHHMM(hhmm); ok {
				return kind, n
			}
		}
		return kind, fallbackValue
	default:
		return "", fallbackValue
	}
}

func numericScheduleValue(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

func parseHHMM(v string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(v), ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func (h *Handler) ListConnectors(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	page, pageSize := pageAndSize(c)
	var tenantIDs []string
	if !scope.ScopeAll() {
		tenantIDs = scope.AllowedTenantIDs
	}
	status := strings.TrimSpace(c.Query("status"))
	datasetID := strings.TrimSpace(c.Query("dataset_id"))
	list, total, err := h.Service.ListConnectors(c.Request.Context(), scope.ScopeAll(), tenantIDs, status, datasetID, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, list, total, page, pageSize)
}

// ListConnectorProvidersMeta is a platform-wide capability read; no tenant
// scope applies.
func (h *Handler) ListConnectorProvidersMeta(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, h.Service.ConnectorProvidersMeta())
}

func (h *Handler) CreateConnector(c *gin.Context) {
	var req connectorPayloadFromRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40060, "invalid connector payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	sensitive := req.Sensitive != nil && *req.Sensitive
	payload, err := h.Service.SealConnectorApprovalPayload(req.toMap())
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHoldForce(c, model.ApprovalObjectConnector, model.ApprovalActionCreate, "new:"+req.provider(), payload, sensitive) {
		return
	}
	kind, value := req.schedule()
	in := service.ConnectorCreateInput{
		Name:                req.Name,
		Provider:            req.provider(),
		AdapterKind:         req.AdapterKind,
		ExternalConnectorID: req.ExternalConnectorID,
		TargetDatasetID:     req.TargetDataset,
		ScheduleKind:        kind,
		ScheduleValue:       value,
		ScheduleTZOffsetMin: req.tzOffset(),
		Sensitive:           req.Sensitive != nil && *req.Sensitive,
		Enabled:             req.Enabled,
		CreatedBy:           userID,
	}
	if req.Spec != nil {
		if b, err := jsonMarshalMap(req.Spec); err == nil {
			in.SpecJSON = string(b)
		}
	}
	if req.Credential != nil {
		if b, err := jsonMarshalMap(req.Credential); err == nil {
			in.CredentialJSON = string(b)
		}
	}
	view, err := h.Service.CreateConnector(ctx, tenantID, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "connector.create",
		Resource: "connector", ResourceID: view.ID,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

// TestConnector probes an unsaved provider/spec/credential triple (wizard
// validation); the persisted probe lives at POST /connectors/:id/test.
func (h *Handler) TestConnector(c *gin.Context) {
	var req struct {
		Provider   string         `json:"provider"`
		Spec       map[string]any `json:"spec"`
		Credential map[string]any `json:"credential"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40060, "invalid connector test payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	specJSON, _ := jsonMarshalMap(req.Spec)
	credJSON, _ := jsonMarshalMap(req.Credential)
	err := h.Service.TestConnector(ctx, service.ConnectorTestInput{
		Provider: req.Provider, SpecJSON: string(specJSON), CredentialJSON: string(credJSON),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// TestConnectorByID probes a persisted connector (read-only; doc/225 §7.2).
func (h *Handler) TestConnectorByID(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	health, err := h.Service.TestConnectorByID(c.Request.Context(), scope.ScopeAll(), scope.AllowedTenantIDs, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, health)
}

func (h *Handler) GetConnector(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	view, err := h.Service.GetConnectorForScope(c.Request.Context(), scope.ScopeAll(), scope.AllowedTenantIDs, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) UpdateConnector(c *gin.Context) {
	var req connectorPayloadFromRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40060, "invalid connector payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	id := c.Param("id")
	current, gerr := h.Service.GetConnectorForScope(ctx, false, []string{tenantID}, id)
	if gerr != nil {
		response.Err(c, gerr)
		return
	}
	approvalAction := model.ApprovalActionUpdate
	if len(req.Credential) > 0 {
		// Credential rotation is its own approval action (doc/225 §7.1).
		approvalAction = model.ApprovalActionRotateCredential
	}
	// Sensitive sources always gate writes (doc/225 §9), even without a
	// matching policy; the approval payload carries the effective flag.
	payload := req.toMap()
	effectiveSensitive := current.Sensitive
	if req.Sensitive != nil {
		effectiveSensitive = *req.Sensitive
	}
	payload["sensitive"] = effectiveSensitive
	if payload["provider"] == "" {
		payload["provider"] = current.Provider
	}
	payload, err := h.Service.SealConnectorApprovalPayload(payload)
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHoldForce(c, model.ApprovalObjectConnector, approvalAction, id, payload, effectiveSensitive) {
		return
	}
	kind, value := req.schedule()
	in := service.ConnectorUpdateInput{
		Name:                req.Name,
		Provider:            req.provider(),
		AdapterKind:         req.AdapterKind,
		ExternalConnectorID: req.ExternalConnectorID,
		TargetDatasetID:     req.TargetDataset,
		ScheduleKind:        kind,
		ScheduleValue:       value,
		ScheduleTZOffsetMin: req.tzOffset(),
		ScheduleTZOffsetSet: req.TZOffsetMin != nil || req.Schedule != nil,
		Sensitive:           req.Sensitive,
		Enabled:             req.Enabled,
	}
	// PUT is a partial update: an omitted spec/credential must leave the
	// stored value untouched, not overwrite it with an empty object.
	if req.Spec != nil {
		if b, err := jsonMarshalMap(req.Spec); err == nil {
			in.SpecJSON = string(b)
		}
	}
	if req.Credential != nil {
		if b, err := jsonMarshalMap(req.Credential); err == nil {
			in.CredentialJSON = string(b)
		}
	}
	view, err := h.Service.UpdateConnector(ctx, false, []string{tenantID}, id, in)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "connector.update",
		Resource: "connector", ResourceID: view.ID,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

func (h *Handler) DeleteConnector(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	id := c.Param("id")
	current, gerr := h.Service.GetConnectorForScope(ctx, false, []string{tenantID}, id)
	if gerr != nil {
		response.Err(c, gerr)
		return
	}
	payload := map[string]any{"provider": current.Provider, "sensitive": current.Sensitive}
	if h.approvalHoldForce(c, model.ApprovalObjectConnector, model.ApprovalActionDelete, id, payload, current.Sensitive) {
		return
	}
	if err := h.Service.DeleteConnector(ctx, false, []string{tenantID}, id); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "connector.delete",
		Resource: "connector", ResourceID: id,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, gin.H{"ok": true})
}

// SetConnectorEnabled handles both POST /enable and POST /disable; the router
// registers the shared handler under both paths.
func (h *Handler) SetConnectorEnabled(c *gin.Context) {
	disabled := strings.HasSuffix(c.Request.URL.Path, "/disable") || c.Param("state") == "disable"
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	id := c.Param("id")
	current, gerr := h.Service.GetConnectorForScope(ctx, false, []string{tenantID}, id)
	if gerr != nil {
		response.Err(c, gerr)
		return
	}
	payload := map[string]any{"provider": current.Provider, "sensitive": current.Sensitive, "enabled": !disabled}
	if h.approvalHoldForce(c, model.ApprovalObjectConnector, model.ApprovalActionUpdate, id, payload, current.Sensitive) {
		return
	}
	if err := h.Service.SetConnectorEnabled(ctx, false, []string{tenantID}, id, disabled); err != nil {
		response.Err(c, err)
		return
	}
	action := "connector.enable"
	if disabled {
		action = "connector.disable"
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: action,
		Resource: "connector", ResourceID: id,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, gin.H{"ok": true})
}

// SyncConnectorNow enqueues a durable manual sync job.
func (h *Handler) SyncConnectorNow(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	queued, err := h.Service.EnqueueConnectorManualTrigger(ctx, false, []string{tenantID}, c.Param("id"), userID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"queued": queued})
}

// SetConnectorSchedule changes the period or switches between manual/interval/
// daily without touching the source spec (doc/225 §7.2 PUT /schedule).
func (h *Handler) SetConnectorSchedule(c *gin.Context) {
	var req struct {
		Kind        string `json:"kind"`
		Value       int    `json:"value"`
		Minutes     int    `json:"minutes"`
		HHMM        string `json:"hhmm"`
		Schedule    any    `json:"schedule"` // doc/225 §7.2 object form
		TZOffsetMin *int   `json:"schedule_tz_offset_min"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40335, "invalid connector schedule payload")
		return
	}
	kind := strings.TrimSpace(req.Kind)
	value := req.Value
	if req.Schedule != nil {
		kind, value = normalizeConnectorScheduleInput(req.Schedule, value)
	}
	if req.Minutes > 0 {
		value = req.Minutes
	}
	if req.HHMM != "" {
		n, ok := parseHHMM(req.HHMM)
		if !ok {
			response.Fail(c, 400, 40335, "invalid connector schedule payload: hhmm must be HH:MM")
			return
		}
		value = n
	}
	tzOffset := 0
	if req.TZOffsetMin != nil {
		tzOffset = *req.TZOffsetMin
	} else if req.Schedule != nil {
		tzOffset = model.ConnectorDefaultTZOffsetMin
	}
	if req.Schedule != nil && kind == model.ConnectorScheduleDaily {
		value = connectorLocalMinuteToUTC(value, tzOffset)
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	id := c.Param("id")
	current, gerr := h.Service.GetConnectorForScope(ctx, false, []string{tenantID}, id)
	if gerr != nil {
		response.Err(c, gerr)
		return
	}
	payload := map[string]any{
		"provider": current.Provider, "sensitive": current.Sensitive,
		"schedule": kind, "schedule_value": value, "schedule_tz_offset_min": tzOffset,
	}
	if h.approvalHoldForce(c, model.ApprovalObjectConnector, model.ApprovalActionUpdate, id, payload, current.Sensitive) {
		return
	}
	view, err := h.Service.SetConnectorSchedule(ctx, false, []string{tenantID}, id, kind, value, tzOffset)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "connector.schedule",
		Resource: "connector", ResourceID: view.ID, DetailJSON: fmt.Sprintf(`{"kind":%q}`, kind),
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, view)
}

// PreviewConnector dry-runs one scan page and reports the diff (read-only).
func (h *Handler) PreviewConnector(c *gin.Context) {
	var req struct {
		Limit int `json:"limit"`
	}
	_ = c.ShouldBindJSON(&req)
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	preview, err := h.Service.PreviewConnector(ctx, scope.ScopeAll(), scope.AllowedTenantIDs, c.Param("id"), req.Limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, preview)
}

func (h *Handler) ListConnectorRuns(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	page, pageSize := pageAndSize(c)
	var tenantIDs []string
	if !scope.ScopeAll() {
		tenantIDs = scope.AllowedTenantIDs
	}
	list, total, err := h.Service.ListConnectorRuns(c.Request.Context(), scope.ScopeAll(), tenantIDs, c.Param("id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, list, total, page, pageSize)
}

// GetConnectorRun returns one run row (doc/225 §7.2 run detail).
func (h *Handler) GetConnectorRun(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "connector"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "connector")
	if !ok {
		return
	}
	if !h.requireSensitiveGovernanceRead(c, scope) {
		return
	}
	run, err := h.Service.GetConnectorRun(c.Request.Context(), scope.ScopeAll(), scope.AllowedTenantIDs, c.Param("id"), c.Param("runID"))
	if err != nil {
		response.Err(c, err)
		return
	}
	// DetailJSON stays internal on the model (it is not selected into list
	// responses); the run-detail endpoint exposes a parsed view so operators can
	// see per-object skip reasons without leaking raw storage columns.
	detail := map[string]any{}
	if strings.TrimSpace(run.DetailJSON) != "" {
		_ = json.Unmarshal([]byte(run.DetailJSON), &detail)
	}
	response.OK(c, struct {
		*model.ConnectorRun
		Detail map[string]any `json:"detail"`
	}{ConnectorRun: run, Detail: detail})
}

// jsonMarshalMap marshals a map until nil/empty-tolerant semantics.
func jsonMarshalMap(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}
