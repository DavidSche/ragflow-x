package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Default-model write error codes (registered in specification 12 §2.2 Model /
// conflict / upstream sections, doc/269 §4.2).
const (
	defaultModelErrModelTypeInvalid  = 40032 // model_type 非法 / 别名归一后仍不支持
	defaultModelErrModelNotOwned     = 40037 // model_id 不属于本 tenant 已加模型
	defaultModelErrTypeMismatch      = 40038 // 默认模型类型与 model_type 不匹配
	defaultModelErrCapabilityNegated = 40035 // 当前 RAGFlow 版本/构建不支持该默认模型类型
	defaultModelErrMissingCAS        = 40036 // 缺少 expected_model_id CAS 声明
	defaultModelErrCASConflict       = 40912 // expected_model_id 与当前默认不一致
	defaultModelErrLockBusy          = 40913 // 平台级写锁被其它操作占用
	defaultModelErrSetTransport      = 50256 // RAGFlow 设置默认模型失败（透传上游原因）
	defaultModelErrResultUnknown     = 50257 // 结果未知，响应含 operation_ref + status:unknown
	defaultModelErrLockConn          = 50258 // 写锁连接获取失败（DB 故障，非并发冲突）
	defaultModelErrLockSQL           = 50259 // 写锁 SQL 执行失败（DB 故障，非并发冲突）
	defaultModelErrReadFailed        = 50293 // 读取 RAGFlow 默认模型/模型列表失败（不得推断为未设置）
)

// PlatformDefaultModelLockKey is the fixed advisory-lock integer key
// ('MODE' in ASCII). Fixed constant, never a runtime string hash — different
// replicas must converge on the same key (doc/269 §4.2, 评审 P0-1).
const PlatformDefaultModelLockKey int64 = 0x4D4F4445

// Default-model audit action on the platform tenant (doc/269 §4.2).
const defaultModelAuditAction = "model-default.write"

var errDefaultModelLockConn = httperr.New(500, defaultModelErrLockConn, "model-default 写锁失败: acquire dedicated connection")
var errDefaultModelLockSQL = httperr.New(500, defaultModelErrLockSQL, "model-default 写锁失败: tryAdvisoryLock sql error")
var errDefaultModelLockBusy = httperr.New(409, defaultModelErrLockBusy, "model-default 写锁被其它操作占用")
var errDefaultModelMissingCAS = httperr.BadRequest(defaultModelErrMissingCAS, "缺少 expected_model_id CAS 声明：安全写路径必须携带 CAS")

// SetDefaultRequest carries the tenant default-model write contract with the
// CAS expectation (v9, 评审 P0-1). A plain *string cannot distinguish a missing
// JSON field from an explicit null, so ExpectedPresent preserves whether the
// field appeared at all:
//
//	ExpectedPresent == false                       -> caller made no CAS declaration
//	ExpectedPresent == true && ExpectedModelID nil -> expect "no default currently"
//	ExpectedPresent == true && ExpectedModelID set -> expect current == that value
type SetDefaultRequest struct {
	ModelType       string  `json:"model_type"`
	ModelID         string  `json:"model_id"`
	ExpectedModelID *string `json:"expected_model_id"`
	ExpectedPresent bool    `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler. It inspects the top-level keys so
// it can preserve the missing-vs-null distinction (precedent in this repo:
// provider/ragflow/ragflow.go flexString, handler/search_app.go; doc/269 §4.2).
func (r *SetDefaultRequest) UnmarshalJSON(b []byte) error {
	type alias SetDefaultRequest
	var plain alias
	if err := json.Unmarshal(b, &plain); err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return err
	}
	r.ModelType = plain.ModelType
	r.ModelID = plain.ModelID
	if raw, present := top["expected_model_id"]; present {
		r.ExpectedPresent = true
		if strings.TrimSpace(string(raw)) != "null" {
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			r.ExpectedModelID = &v
		}
	}
	return nil
}

// DefaultModelStatus is the per-type current default view consumed by the UI
// (GET defaults four-state algorithm, doc/269 §4.3).
type DefaultModelStatus struct {
	ModelType    string `json:"model_type"`
	ModelID      string `json:"model_id,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
	Status       string `json:"status"`    // ok | invalid | verify_failed | unset
	Supported    bool   `json:"supported"` // capability gate: type settable on this build
	Error        string `json:"error,omitempty"`
}

// DefaultModelOption is one selectable model for a default-model type, used to
// build the Provider → Instance → Model tree in the default-model panel
// (doc/269 §4.4). Exposes model identity only — never provider config/keys.
type DefaultModelOption struct {
	ModelID  string `json:"model_id"`
	Name     string `json:"name"`
	Provider string `json:"provider_name"`
	Instance string `json:"instance_name"`
}

// ListDefaultModelOptions returns, per canonical default-model type, the
// tenant models that are candidate defaults (doc/269 §4.4 data source). It is
// read-only and follows the same per-type ListModels walk as the four-state read.
func (s *Service) ListDefaultModelOptions(ctx context.Context) (map[string][]DefaultModelOption, error) {
	out := make(map[string][]DefaultModelOption, len(canonicalDefaultModelTypes))
	for _, t := range canonicalDefaultModelTypes {
		models, err := s.RAGFlow.ListModels(ctx, t)
		if err != nil {
			return nil, httperr.New(502, defaultModelErrReadFailed, "读取 RAGFlow "+t+" 模型列表失败")
		}
		opts := make([]DefaultModelOption, 0, len(models))
		for _, m := range models {
			opts = append(opts, DefaultModelOption{
				ModelID:  m.ModelID,
				Name:     m.Name,
				Provider: m.Provider,
				Instance: m.Instance,
			})
		}
		out[t] = opts
	}
	return out, nil
}

// DefaultModelWriteResult is the PATCH /model-providers/defaults response.
type DefaultModelWriteResult struct {
	OperationRef string  `json:"operation_ref"`
	ModelType    string  `json:"model_type"`
	Previous     *string `json:"previous_model_id"`
	Observed     *string `json:"observed_model_id"`
	Status       string  `json:"status"` // succeeded | failed | unknown (audit Result)
	Message      string  `json:"message,omitempty"`
}

// canonicalizeDefaultModelType normalises provider-boundary aliases (asr /
// vision / img2txt) to the canonical vocabulary. Returns ok=false for a string
// that is not a canonical type at all.
func canonicalizeDefaultModelType(t string) (string, bool) {
	switch t {
	case "chat", "embedding", "rerank", "speech2text", "image2text", "tts", "ocr", "decision":
		return t, true
	case "asr":
		return "speech2text", true
	case "vision", "img2txt":
		return "image2text", true
	default:
		return "", false
	}
}

// getCurrentDefault returns the tenant's current default model_id for the
// canonical model type ("" when unset), plus the upstream failure, if any.
func (s *Service) getCurrentDefault(ctx context.Context, modelType string) (string, error) {
	defaults, err := s.RAGFlow.ListDefaultModels(ctx)
	if err != nil {
		return "", err
	}
	for _, dm := range defaults {
		if dm.Type == modelType {
			return dm.ModelID, nil
		}
	}
	return "", nil
}

// SetTenantDefaultModel sets or clears the platform default model for a model
// type on the shared RAGFlow tenant. actorTenantID / actorUserID come from the
// request identity (JWT, never role claims, spec 11 §1.2).
//
// Flow (doc/269 §4.2): Authorize(manage, model-default) → platform advisory
// lock → capability check → write-front snapshot → CAS check → append pending
// audit row A → upstream PATCH → write-back read → append terminal audit row B
// (succeeded / failed / unknown). The audit table is append-only: B is a NEW
// row, A is never updated.
func (s *Service) SetTenantDefaultModel(ctx context.Context, actorTenantID, actorUserID, modelType, modelID string, req *SetDefaultRequest) (*DefaultModelWriteResult, error) {
	// 0) Authorisation — single authoritative RBAC entry (service-side review of
	//    the handler's check, same resource+action semantics; no role names).
	if err := s.Authorize(ctx, actorUserID, "manage", "model-default"); err != nil {
		return nil, err
	}
	// Alias normalisation at the service boundary (provider re-converges too).
	canonical, ok := canonicalizeDefaultModelType(modelType)
	if !ok {
		return nil, httperr.BadRequest(defaultModelErrModelTypeInvalid, "model_type 非法（支持 chat/embedding/rerank/speech2text/image2text/tts/ocr/decision）")
	}
	modelType = canonical

	// 1) Platform writer lock — dedicated conn + pg_try_advisory_lock; defer
	//    release right after acquire covers panic and every return path
	//    (评审 P0-3: HTTP timeout never auto-releases the lock).
	release, err := s.withModelDefaultLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	// 3) Capability gate (v9, doc/269 §4.5): unsupported on this build → 400xx,
	//    never forwarded confused upstream errors.
	if !s.defaultModelTypeSupported(ctx, modelType) {
		return nil, httperr.New(400, defaultModelErrCapabilityNegated,
			"当前 RAGFlow 版本/构建不支持设置 "+modelType+" 默认模型")
	}

	// 4) Write-front snapshot (previous) for CAS + audit attribution.
	previousID, err := s.getCurrentDefault(ctx, modelType)
	if err != nil {
		return nil, s.defaultReadFailure(err)
	}

	// 5) CAS check (v9 three-state).
	if req != nil && req.ExpectedPresent {
		expectNil := req.ExpectedModelID == nil
		ok := (expectNil && previousID == "") || (!expectNil && req.ExpectedModelID != nil && previousID == *req.ExpectedModelID)
		if !ok {
			return nil, httperr.New(409, defaultModelErrCASConflict,
				"expected_model_id 与当前默认不一致（并发写/其它入口已改变当前默认），请重新读取当前状态后重试")
		}
	} else {
		// Safe-write path must carry CAS; an internal management override is the
		// only caller allowed to skip it and none exists yet.
		return nil, errDefaultModelMissingCAS
	}

	// 6) Append pending audit row A BEFORE touching upstream, and only proceed
	//    once it is durable (no silent window where upstream changes but no
	//    pending trail exists).
	operationRef := newDefaultModelOperationRef(modelType)
	if err := s.appendDefaultModelAudit(ctx, actorTenantID, actorUserID, modelType, operationRef, "pending",
		map[string]interface{}{
			"operation_ref":      operationRef,
			"requested_model_id": modelID,
			"previous_model_id":  previousID,
			"model_type":         modelType,
			"operator_id":        actorUserID,
		}); err != nil {
		return nil, httperr.Internal("默认模型写审计(pending)落库失败，未执行上游写入")
	}

	// Resolve modelID against the tenant's added models (friendly pre-check
	// only; upstream remains authoritative, review P1-5 TOCTOU is upstream's).
	if modelID != "" {
		if err := s.validateOwnedDefaultModel(ctx, modelType, modelID); err != nil {
			_ = s.appendDefaultModelAudit(ctx, actorTenantID, actorUserID, modelType, operationRef, "failed",
				map[string]interface{}{
					"operation_ref":     operationRef,
					"observed_model_id": "",
					"operation_status":  "failed",
					"error_reason":      err.Error(),
				})
			return nil, err
		}
	}

	// 7) Upstream PATCH (bounded HTTP timeout inside the client transport).
	patchErr := s.RAGFlow.SetDefaultModel(ctx, ragflow.SetDefaultModelRequest{
		ModelType: modelType,
		ModelID:   modelID,
	})

	// 8) Write-back read → observed, under the lock (评审 P0-2 / P0-3): this
	//    attributes the outcome to this operation because we are still the only
	//    writer (attribution table, 评审 P0-4).
	observedID, readErr := s.getCurrentDefault(ctx, modelType)

	result := &DefaultModelWriteResult{
		OperationRef: operationRef,
		ModelType:    modelType,
		Previous:     defaultModelStringPtr(previousID),
		Observed:     nil,
		Status:       "unknown",
	}

	switch {
	case patchErr != nil && isUpstreamBusinessTypeInvalid(patchErr):
		// 9a) Upstream explicitly rejected the type — capability negative feedback
		//     (doc/269 §4.5), never a 5xx (评审 P0-4/P1-1).
		s.recordDefaultModelTypeInvalid(ctx, modelType)
		status := "failed"
		result.Status = status
		result.Observed = defaultModelStringPtr(previousID)
		result.Message = patchErr.Error()
		_ = s.appendDefaultModelAudit(ctx, actorTenantID, actorUserID, modelType, operationRef, status,
			map[string]interface{}{
				"operation_ref":     operationRef,
				"observed_model_id": previousID,
				"operation_status":  status,
				"error_reason":      patchErr.Error(),
			})
		return nil, httperr.New(400, defaultModelErrCapabilityNegated, patchErr.Error())

	case patchErr != nil:
		// 9b) Transport failure: the PATCH may have landed upstream. Re-read
		//     under the lock to attribute (评审 P0-3/P1-7): new value → succeeded,
		//     old value → failed, read failure → unknown. Never blind-retry.
		switch {
		case readErr != nil:
			result.Status = "unknown"
			result.Observed = nil
			result.Message = "无法确认上游默认模型设置结果，请重新读取当前状态（不得自动重试 PATCH）"
		case observedID != previousID:
			// The value moved away from the pre-write snapshot: the write landed
			// even though the response was lost.
			result.Status = "succeeded"
			result.Observed = defaultModelStringPtr(observedID)
			result.Message = ""
		default:
			// Value unchanged → the write did not take effect.
			result.Status = "failed"
			result.Observed = defaultModelStringPtr(observedID)
			result.Message = patchErr.Error()
		}
		_ = s.appendDefaultModelAudit(ctx, actorTenantID, actorUserID, modelType, operationRef, result.Status,
			map[string]interface{}{
				"operation_ref":     operationRef,
				"observed_model_id": result.Observed,
				"operation_status":  result.Status,
				"error_reason":      result.Message,
			})
		if result.Status == "succeeded" {
			// Successful attribution after a transport failure: carry the write
			// result (never (nil, nil), review P0-3 attribution table).
			return result, nil
		}
		return nil, s.defaultWriteFailure(result)

	default:
		// 9c) PATCH succeeded at HTTP/business level; observe the write-back to
		//     confirm and attribute (review P0-2).
		if readErr != nil {
			result.Status = "unknown"
			result.Observed = nil
			result.Message = "上游已收到设置但写后回读失败，无法确认结果"
		} else if observedID == modelID || (modelID == "" && observedID == "") {
			result.Status = "succeeded"
			result.Observed = defaultModelStringPtr(observedID)
			result.Message = ""
		} else {
			// Upstream accepted but the write-back does not match — treat as unknown
			// (could be external interference), never fabricate success.
			result.Status = "unknown"
			result.Observed = defaultModelStringPtr(observedID)
			result.Message = "写后回读与期望不一致"
		}
		_ = s.appendDefaultModelAudit(ctx, actorTenantID, actorUserID, modelType, operationRef, result.Status,
			map[string]interface{}{
				"operation_ref":     operationRef,
				"observed_model_id": result.Observed,
				"operation_status":  result.Status,
				"error_reason":      result.Message,
			})
		if result.Status == "unknown" {
			return nil, httperr.New(502, defaultModelErrResultUnknown, result.Message)
		}
	}
	return result, nil
}

// GetTenantDefaultModels implements the GET defaults four-state algorithm
// (doc/269 §4.3): ok / invalid / verify_failed / unset, keyed on the default
// model_id. ListDefaultModels failure is an overall read failure — NEVER
// inferred as unset (评审 P1-2). Response contains identity only, never
// provider config / API keys.
func (s *Service) GetTenantDefaultModels(ctx context.Context) ([]DefaultModelStatus, error) {
	defaults, err := s.RAGFlow.ListDefaultModels(ctx)
	if err != nil {
		return nil, httperr.New(502, defaultModelErrReadFailed, "读取 RAGFlow 默认模型失败，无法确认当前状态（不得推断为未设置）")
	}
	byType := map[string]ragflow.DefaultModel{}
	for _, dm := range defaults {
		byType[dm.Type] = dm
	}
	out := make([]DefaultModelStatus, 0, len(canonicalDefaultModelTypes))
	for _, t := range canonicalDefaultModelTypes {
		dm, has := byType[t]
		if !has || (has && dm.ModelID == "") {
			out = append(out, DefaultModelStatus{ModelType: t, Status: "unset", Supported: s.defaultModelTypeSupported(ctx, t)})
			continue
		}
		models, listErr := s.RAGFlow.ListModels(ctx, t)
		if listErr != nil {
			out = append(out, DefaultModelStatus{ModelType: t, ModelID: dm.ModelID, Status: "verify_failed", Supported: s.defaultModelTypeSupported(ctx, t), Error: "上游模型列表查询失败，状态未知"})
			continue
		}
		found := false
		var name, provider, instance string
		for _, m := range models {
			if m.ModelID == dm.ModelID {
				found = true
				name, provider, instance = m.Name, m.Provider, m.Instance
				break
			}
		}
		if found {
			out = append(out, DefaultModelStatus{ModelType: t, ModelID: dm.ModelID, ModelName: name, ProviderName: provider, InstanceName: instance, Status: "ok", Supported: s.defaultModelTypeSupported(ctx, t)})
		} else {
			out = append(out, DefaultModelStatus{ModelType: t, ModelID: dm.ModelID, Status: "invalid", Supported: s.defaultModelTypeSupported(ctx, t), Error: "默认模型已被删除/禁用，建议重新设置"})
		}
	}
	return out, nil
}

// ReconcileModelDefaultRecovery is the periodic compensator (doc/269 §4.2
// recovery rules, 评审 P0-3/P0-4/P1-3): it scans pending default-model audit
// rows older than the threshold, idempotently claims one per operation_ref on
// the independent claim table (ON CONFLICT DO NOTHING — audit table stays pure
// append, v9 self-audit ⑧(a)), confirms no worker holds the platform lock, and
// appends the terminal row B from an out-of-lock upstream read. It never
// replays the PATCH and never UPDATEs audit rows. Returns the number of claims
// finalised.
func (s *Service) ReconcileModelDefaultRecovery(ctx context.Context, pendingBefore time.Time) (int, error) {
	rows, err := s.Store.ListPendingModelDefaultAudits(ctx, pendingBefore)
	if err != nil {
		return 0, err
	}
	finalised := 0
	for _, row := range rows {
		opRef, modelType, err := parseDefaultModelOperation(row.DetailJSON)
		if err != nil || opRef == "" {
			continue
		}
		// Idempotent single-worker claim.
		inserted, err := s.Store.CreateModelDefaultRecoveryClaim(ctx, &model.ModelDefaultRecoveryClaim{
			OperationRef: opRef,
			WorkerID:     id.New(),
		})
		if err != nil {
			return finalised, err
		}
		if !inserted {
			continue // another worker or earlier run holds the claim
		}
		// Terminal row already present (produces appending worker) → just finish.
		hasTerminal, err := s.Store.HasDefaultModelTerminalAudit(ctx, opRef)
		if err != nil {
			return finalised, err
		}
		if hasTerminal {
			_ = s.Store.FinishModelDefaultRecoveryClaim(ctx, opRef)
			finalised++
			continue
		}
		// Confirm the original operation is no longer running and finalise it.
		// The helper owns the lock lifecycle (defer release) — a discarded release
		// would deadlock the in-process fallback and starve the advisory lock.
		done, err := s.finaliseDefaultModelRecovery(ctx, row, opRef, modelType)
		if err != nil {
			if errors.Is(err, errDefaultModelLockBusy) {
				continue // a writer is active; retry this operation next round
			}
			return finalised, err // DB/lock infrastructure failure: surface it
		}
		if !done {
			continue
		}
		_ = s.Store.FinishModelDefaultRecoveryClaim(ctx, opRef)
		finalised++
	}
	return finalised, nil
}

// finaliseDefaultModelRecovery appends the terminal audit row B for one claimed
// pending operation. It runs under the platform write lock (proving no live
// writer is in flight) and always releases it.
func (s *Service) finaliseDefaultModelRecovery(ctx context.Context, row model.AuditLog, opRef, modelType string) (bool, error) {
	release, err := s.withModelDefaultLock(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	// Out-of-lock upstream read (we hold the lock now, so no writer can be
	// active; external clients are outside our protection, hence the strict
	// attribution below).
	current, readErr := s.getCurrentDefault(ctx, modelType)
	requested := requestedModelID(row.DetailJSON)
	var result string
	switch {
	case readErr != nil:
		result = "unknown"
	case current == requested:
		// Cannot attribute "current value equals requested" — an external
		// admin/other client may have set the same value (评审 P0-4).
		result = "unknown"
	case modelType == "" || requested == "":
		result = "unknown"
	default:
		// current != requested and the operation's requested value never
		// landed → the operation did not take effect.
		result = "failed"
	}
	_ = s.appendDefaultModelAudit(ctx, row.ActorTenantID, row.UserID, modelType, opRef, result,
		map[string]interface{}{
			"operation_ref":     opRef,
			"observed_model_id": current,
			"operation_status":  result,
			"error_reason":      "recovery compensator 核实（锁外，无法归因时从严 unknown）",
		})
	return true, nil
}

// --- internals --------------------------------------------------------------

func (s *Service) defaultReadFailure(err error) error {
	return httperr.New(502, defaultModelErrReadFailed, "读取 RAGFlow 默认模型失败")
}

// defaultWriteFailure converts a non-succeeded write result into the transport
// error code. It deliberately has no success path: the caller returns the
// succeeded result directly, so returning nil here would resurrect the
// (nil, nil) attribution bug (评审 P0-3).
func (s *Service) defaultWriteFailure(result *DefaultModelWriteResult) error {
	if result.Status == "failed" {
		return httperr.New(502, defaultModelErrSetTransport, result.Message)
	}
	return httperr.New(502, defaultModelErrResultUnknown, result.Message)
}

// validateOwnedDefaultModel pre-checks that modelID is one of the tenant's
// added models of the matching canonical type (friendly error before touching
// upstream; upstream remains authoritative — review P1-5).
func (s *Service) validateOwnedDefaultModel(ctx context.Context, modelType, modelID string) error {
	models, err := s.RAGFlow.ListModels(ctx, modelType)
	if err != nil {
		return httperr.New(502, defaultModelErrReadFailed, "读取 RAGFlow "+modelType+" 模型列表失败")
	}
	for _, m := range models {
		if m.ModelID == modelID {
			return nil
		}
	}
	// Not in the requested type list: if it is an owned model under a DIFFERENT
	// canonical type, report a type mismatch (40038). Only if it is owned
	// nowhere does the model_id truly not belong to this tenant (40037).
	if otherType := s.ownedModelOtherType(ctx, modelType, modelID); otherType != "" {
		return httperr.BadRequest(defaultModelErrTypeMismatch,
			fmt.Sprintf("model_id 属于本 tenant 但类型为 %s，与请求的 %s 不匹配", otherType, modelType))
	}
	return httperr.BadRequest(defaultModelErrModelNotOwned, "model_id 不属于本 tenant 已加模型")
}

// ownedModelOtherType does a best-effort cross-type lookup of modelID among the
// other canonical default types; returns the first type that owns it ("" = none).
func (s *Service) ownedModelOtherType(ctx context.Context, excludeType, modelID string) string {
	for _, t := range canonicalDefaultModelTypes {
		if t == excludeType {
			continue
		}
		others, err := s.RAGFlow.ListModels(ctx, t)
		if err != nil {
			continue // best-effort: one failing type must not defeat attribution
		}
		for _, m := range others {
			if m.ModelID == modelID {
				return t
			}
		}
	}
	return ""
}

// appendDefaultModelAudit appends one audit row (A pending, or terminal B) on
// the platform tenant with the model-default contract. Pending/terminal share
// operation_ref; each row is a new append (never an UPDATE).
func (s *Service) appendDefaultModelAudit(ctx context.Context, actorTenantID, userID, modelType, operationRef, result string, detail map[string]interface{}) error {
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	entry := &model.AuditLog{
		TenantID:      model.PlatformTenantID,
		ActorTenantID: actorTenantID,
		UserID:        userID,
		Action:        defaultModelAuditAction,
		Resource:      "model-default",
		ResourceID:    "model-default:" + modelType,
		DetailJSON:    string(detailJSON),
		TraceID:       operationRef,
		Scope:         "platform",
		Result:        result,
	}
	return s.RecordAudit(ctx, entry)
}

func newDefaultModelOperationRef(modelType string) string {
	return "md:" + modelType + ":" + id.New()
}

func defaultModelStringPtr(v string) *string { return &v }

// isUpstreamBusinessTypeInvalid reports whether the upstream error is an
// explicit "model type X is invalid" business rejection (the only class that
// must feed capability negative feedback; doc/269 §4.5 — plain missing-model,
// type-mismatch and auth errors never do).
func isUpstreamBusinessTypeInvalid(err error) bool {
	var ragErr *ragflow.Error
	if !errors.As(err, &ragErr) {
		return false
	}
	if ragErr.Type != ragflow.ErrorTypeBusiness {
		return false
	}
	lower := strings.ToLower(ragErr.Message)
	return (strings.Contains(lower, "is invalid") && strings.Contains(lower, "model type")) ||
		strings.Contains(lower, "无法用作") || strings.Contains(lower, "cannot be used as")
}

// parseDefaultModelOperation extracts operation_ref + model_type from an audit
// row's DetailJSON (goes through the claim/terminal lookups).
func parseDefaultModelOperation(detailJSON string) (opRef, modelType string, err error) {
	var d map[string]interface{}
	if err = json.Unmarshal([]byte(detailJSON), &d); err != nil {
		return "", "", err
	}
	if v, ok := d["operation_ref"].(string); ok {
		opRef = v
	}
	if v, ok := d["model_type"].(string); ok {
		modelType = v
	}
	return opRef, modelType, nil
}

func requestedModelID(detailJSON string) string {
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(detailJSON), &d); err != nil {
		return ""
	}
	if v, ok := d["requested_model_id"].(string); ok {
		return v
	}
	return ""
}

// modelDefaultPGConn is the physical-destroy surface we need from the pgx
// stdlib connection: a Close(ctx) on the underlying *pgx.Conn. Matching it
// through a tiny interface (not an import of pgx/stdlib) keeps the service
// boundary driver-agnostic.
type modelDefaultPGConn interface {
	Close(ctx context.Context) error
}

// withModelDefaultLock acquires the platform writer lock for default models
// (doc/269 §4.2). It probes the pool driver once per acquisition: pgx (real
// postgres, via repository.RawDBProvider) → dedicated sql.Conn +
// pg_try_advisory_lock with the fixed key and physical-destroy-on-unlock-
// failure; sqlite / fake stores → in-process mutex (no cross-instance claim,
// documented limitation).
func (s *Service) withModelDefaultLock(ctx context.Context) (_ func(), _ error) {
	raw := s.sqlDB()
	if raw == nil {
		s.modelDefaultProcessMu.Lock()
		return s.modelDefaultProcessMu.Unlock, nil
	}
	conn, err := raw.Conn(ctx)
	if err != nil {
		return nil, errDefaultModelLockConn
	}
	isPG := false
	_ = conn.Raw(func(dc any) error {
		_, isPG = dc.(interface{ Conn() modelDefaultPGConn })
		return nil
	})
	if !isPG {
		_ = conn.Close() // sqlite / fake pool → in-process fallback
		s.modelDefaultProcessMu.Lock()
		return s.modelDefaultProcessMu.Unlock, nil
	}
	return acquirePGDefaultModelLock(ctx, conn, PlatformDefaultModelLockKey)
}

// acquirePGDefaultModelLock implements the postgres advisory-lock path on an
// already-acquired dedicated conn.
func acquirePGDefaultModelLock(ctx context.Context, conn *sql.Conn, key int64) (_ func(), _ error) {
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, errDefaultModelLockSQL
	}
	if !acquired {
		_ = conn.Close() // no lock held → safe to return to the pool (评审 P0-2)
		return nil, errDefaultModelLockBusy
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			uctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var unlocked bool
			if err := conn.QueryRowContext(uctx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil || !unlocked {
				// Unlock failed / unknown → the session may still hold the lock.
				// NEVER return this connection to the pool: destroy the physical
				// session so PostgreSQL frees the advisory lock (评审 P0-2 core).
				_ = conn.Raw(func(dc any) error {
					if pg, ok := dc.(interface{ Conn() modelDefaultPGConn }); ok {
						return pg.Conn().Close(context.Background())
					}
					return nil
				})
				_ = conn.Close()
				logger.Error("model-default 写锁 unlock 失败，物理连接已销毁；advisory lock 由 PostgreSQL 断开连接自动释放")
				return
			}
			if cerr := conn.Close(); cerr != nil {
				logger.Warn("model-default 写锁连接归还失败", "error", cerr)
			}
		})
	}
	return release, nil
}

// sqlDB returns the underlying *sql.DB when the store is gorm-backed, or nil
// (fake stores / tests) which selects the in-process path.
func (s *Service) sqlDB() *sql.DB {
	if p, ok := s.Store.(repository.RawDBProvider); ok {
		return p.RawDB()
	}
	return nil
}
