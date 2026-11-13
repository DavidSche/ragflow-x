package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// defaultModelTestClient wraps the mock provider so tests can inject model
// lists, upstream write failures and list failures per call.
type defaultModelTestClient struct {
	ragflow.Client
	mu           sync.Mutex
	listModels   func(ctx context.Context, modelType string) ([]ragflow.AddedModel, error)
	setDefault   func(ctx context.Context, req ragflow.SetDefaultModelRequest) error
	listDefaults func(ctx context.Context) ([]ragflow.DefaultModel, error)
	setCalls     int
}

func (c *defaultModelTestClient) ListModels(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
	if c.listModels != nil {
		return c.listModels(ctx, modelType)
	}
	return c.Client.ListModels(ctx, modelType)
}

func (c *defaultModelTestClient) SetDefaultModel(ctx context.Context, req ragflow.SetDefaultModelRequest) error {
	c.mu.Lock()
	c.setCalls++
	c.mu.Unlock()
	if c.setDefault != nil {
		return c.setDefault(ctx, req)
	}
	return c.Client.SetDefaultModel(ctx, req)
}

func (c *defaultModelTestClient) ListDefaultModels(ctx context.Context) ([]ragflow.DefaultModel, error) {
	if c.listDefaults != nil {
		return c.listDefaults(ctx)
	}
	return c.Client.ListDefaultModels(ctx)
}

// defaultModelEnv builds a real sqlite-backed service (RBAC + audit work
// through the real store) wired to a mock RAGFlow exposing one chat + one
// embedding tenant model.
type defaultModelEnv struct {
	svc       *Service
	client    *defaultModelTestClient
	userID    string
	tenantID  string
	chatModel string
	embModel  string
}

func newDefaultModelEnv(t *testing.T) *defaultModelEnv {
	t.Helper()
	svc := newUninitializedSvc(t)
	ctx := context.Background()
	if err := svc.CreateFirstAdmin(ctx, "root", "strong-password"); err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	u, err := svc.Store.GetUserByUsername(ctx, "root")
	if err != nil || u == nil {
		t.Fatalf("first admin lookup: %v", err)
	}
	env := &defaultModelEnv{
		svc:       svc,
		userID:    u.ID,
		tenantID:  u.TenantID,
		chatModel: "chat-model-1",
		embModel:  "embed-model-1",
	}
	env.client = &defaultModelTestClient{
		Client: svc.RAGFlow,
		listModels: func(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
			switch modelType {
			case "chat":
				return []ragflow.AddedModel{{ModelID: env.chatModel, Name: "chat-model-1", Type: []string{"chat"}, Provider: "test", Instance: "p1"}}, nil
			case "embedding":
				return []ragflow.AddedModel{{ModelID: env.embModel, Name: "embed-model-1", Type: []string{"embedding"}, Provider: "test", Instance: "p1"}}, nil
			default:
				return nil, nil
			}
		},
	}
	svc.RAGFlow = env.client
	return env
}

// expectUnset is the CAS declaration "there is currently no default".
func expectUnset() *SetDefaultRequest {
	r := &SetDefaultRequest{}
	r.ExpectedPresent = true // key present with null value → expect unset
	r.ExpectedModelID = nil
	return r
}

func expectValue(v string) *SetDefaultRequest {
	r := expectUnset()
	r.ExpectedModelID = &v
	return r
}

// errCode extracts the httperr code, or -1.
func defaultModelErrCode(err error) int {
	var he *httperr.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return -1
}

func TestModelDefaultSetupSetSuccessWithCAS(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	// (a) CAS declaration missing → refused before any upstream write.
	res, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, &SetDefaultRequest{})
	if defaultModelErrCode(err) != defaultModelErrMissingCAS {
		t.Fatalf("missing CAS: res=%+v err=%v (code=%d)", res, err, defaultModelErrCode(err))
	}
	if env.client.setCalls != 0 {
		t.Fatalf("upstream written despite missing CAS: %d calls", env.client.setCalls)
	}

	// (b) CAS expect unset, then set chat default.
	res, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
	if err != nil {
		t.Fatalf("set chat default: %v", err)
	}
	if res.Status != "succeeded" || res.Observed == nil || *res.Observed != env.chatModel {
		t.Fatalf("set result: %+v", res)
	}
	if res.Previous == nil || *res.Previous != "" {
		t.Fatalf("previous should be empty string (was unset), got %v", res.Previous)
	}

	// (c) Two-row append-only trail (A pending + B terminal), same operation_ref.
	if err := assertDefaultModelAuditPair(ctx, env.svc, res.OperationRef); err != nil {
		t.Fatal(err)
	}

	// (d) CAS mismatch (current==chat, expect embedding) → 40920.
	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.embModel, expectValue(env.embModel))
	if defaultModelErrCode(err) != defaultModelErrCASConflict {
		t.Fatalf("CAS mismatch: err=%v (code=%d)", err, defaultModelErrCode(err))
	}

	// (e) Clear with expect current == chat-model-1 → succeeded, observed "".
	res, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", "", expectValue(env.chatModel))
	if err != nil {
		t.Fatalf("clear chat default: %v", err)
	}
	if res.Status != "succeeded" || res.Observed == nil || *res.Observed != "" {
		t.Fatalf("clear result: %+v", res)
	}
}

func TestModelDefaultTypeInvalidAndCapabilityNegated(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	// Unknown canonical miss → 40032 before any upstream call.
	_, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "not-a-type", "x", expectUnset())
	if defaultModelErrCode(err) != defaultModelErrModelTypeInvalid {
		t.Fatalf("unknown type: err=%v (code=%d)", err, defaultModelErrCode(err))
	}

	// decision on an official v1.0.0 baseline (mock enforces the same) → 40035.
	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "decision", "d-model", expectUnset())
	if defaultModelErrCode(err) != defaultModelErrCapabilityNegated {
		t.Fatalf("decision on v1.0.0: err=%v (code=%d)", err, defaultModelErrCode(err))
	}

	// model_id not in the tenant's added models → 40037 (friendly pre-check).
	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", "ghost-model", expectUnset())
	if defaultModelErrCode(err) != defaultModelErrModelNotOwned {
		t.Fatalf("ghost model: err=%v (code=%d)", err, defaultModelErrCode(err))
	}

	// model_id OWNED but under a different type (rerank) → 40038 type mismatch.
	rerankModel := "rerank-model-1"
	env.client.mu.Lock()
	oldList := env.client.listModels
	env.client.listModels = func(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
		if modelType == "rerank" {
			return []ragflow.AddedModel{{ModelID: rerankModel, Name: rerankModel, Type: []string{"rerank"}, Provider: "test", Instance: "p1"}}, nil
		}
		if oldList != nil {
			return oldList(ctx, modelType)
		}
		return nil, nil
	}
	env.client.mu.Unlock()
	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", rerankModel, expectUnset())
	if defaultModelErrCode(err) != defaultModelErrTypeMismatch {
		t.Fatalf("type mismatch: err=%v (code=%d)", err, defaultModelErrCode(err))
	}
}

func TestModelDefaultGetFourStates(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	states, err := env.svc.GetTenantDefaultModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) == 0 {
		t.Fatal("no status rows")
	}
	for _, st := range states {
		if st.Status != "unset" {
			t.Fatalf("fresh state %s = %s, want unset", st.ModelType, st.Status)
		}
		// Capability gate: v1.0.0 has no decision support, so the row must be
		// reported but marked unsupported (UI hides it, doc/269 §4.4).
		if st.ModelType == "decision" && st.Supported {
			t.Fatal("decision must report supported=false on a v1.0.0 build")
		}
		if st.ModelType == "chat" && !st.Supported {
			t.Fatal("chat must report supported=true")
		}
	}

	if _, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset()); err != nil {
		t.Fatalf("set chat: %v", err)
	}
	states, err = env.svc.GetTenantDefaultModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var chatOK, embUnset bool
	for _, st := range states {
		switch st.ModelType {
		case "chat":
			chatOK = st.Status == "ok" && st.ModelID == env.chatModel && st.ModelName != ""
		case "embedding":
			embUnset = st.Status == "unset"
		}
	}
	if !chatOK {
		t.Fatalf("chat not ok: %+v", states)
	}
	if !embUnset {
		t.Fatalf("embedding should be unset: %+v", states)
	}

	// Default references a model that vanished from ListModels → invalid.
	env.client.mu.Lock()
	oldList := env.client.listModels
	env.client.listModels = func(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
		return nil, nil
	}
	env.client.mu.Unlock()
	defer func() { env.client.listModels = oldList }()

	states, err = env.svc.GetTenantDefaultModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chatInvalid := false
	for _, st := range states {
		if st.ModelType == "chat" && st.Status == "invalid" {
			chatInvalid = true
		}
	}
	if !chatInvalid {
		t.Fatalf("expected chat invalid after model removed, states=%+v", states)
	}
}

func TestModelDefaultTransportAttribution(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	// Case 1: upstream write errors out BUT the value actually landed (response
	// lost). Write-back read observes the new value → attributed succeeded.
	env.client.mu.Lock()
	env.client.setDefault = func(ctx context.Context, req ragflow.SetDefaultModelRequest) error {
		_ = env.client.Client.SetDefaultModel(ctx, req) // simulate landed
		return errors.New("upstream transport timeout")
	}
	env.client.mu.Unlock()

	res, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
	if err != nil {
		t.Fatalf("attribution-success run: unexpected hard error %v", err)
	}
	if res.Status != "succeeded" || res.Observed == nil || *res.Observed != env.chatModel {
		t.Fatalf("attribution success: %+v", res)
	}

	// Case 2: transport error + write-back read also fails → unknown / 50257.
	env.client.mu.Lock()
	env.client.setDefault = func(ctx context.Context, req ragflow.SetDefaultModelRequest) error {
		return errors.New("upstream transport timeout")
	}
	env.client.mu.Unlock()

	// previous read must succeed; re-read (after PATCH) must fail.
	failOnRead2 := true
	env.client.listDefaults = func(ctx context.Context) ([]ragflow.DefaultModel, error) {
		if failOnRead2 {
			failOnRead2 = false
			return env.client.Client.ListDefaultModels(ctx)
		}
		return nil, errors.New("upstream list down")
	}

	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "embedding", env.embModel, expectUnset())
	if defaultModelErrCode(err) != defaultModelErrResultUnknown {
		t.Fatalf("unknown attribution: err=%v (code=%d)", err, defaultModelErrCode(err))
	}
}

func TestModelDefaultUpstreamTypeInvalidFeedsCapabilityNegative(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	rejecting := true
	env.client.mu.Lock()
	env.client.setDefault = func(ctx context.Context, req ragflow.SetDefaultModelRequest) error {
		if rejecting {
			return &ragflow.Error{
				Type:       ragflow.ErrorTypeBusiness,
				HTTPStatus: 200,
				Code:       101,
				Message:    "model type chat is invalid",
			}
		}
		return nil
	}
	env.client.mu.Unlock()

	_, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
	if defaultModelErrCode(err) != defaultModelErrCapabilityNegated {
		t.Fatalf("type-invalid feedback: err=%v (code=%d)", err, defaultModelErrCode(err))
	}
	firstCalls := env.client.setCalls

	// Negative binding now short-circuits before any upstream write.
	env.client.mu.Lock()
	rejecting = false
	env.client.mu.Unlock()
	_, err = env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
	if defaultModelErrCode(err) != defaultModelErrCapabilityNegated {
		t.Fatalf("negative binding should block: err=%v (code=%d)", err, defaultModelErrCode(err))
	}
	if env.client.setCalls != firstCalls {
		t.Fatalf("upstream called %d more times despite negative binding", env.client.setCalls-firstCalls)
	}
}

func TestModelDefaultRecoveryCompensatorIdempotent(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	// Create a stale pending row by hand (a writer that died between A and B).
	staleRef := "md:chat:stale-op"
	if err := env.svc.appendDefaultModelAudit(ctx, env.tenantID, env.userID, "chat", staleRef, "pending", map[string]interface{}{
		"operation_ref":      staleRef,
		"requested_model_id": env.chatModel,
		"previous_model_id":  "",
		"model_type":         "chat",
	}); err != nil {
		t.Fatal(err)
	}

	pendingBefore := time.Now().Add(time.Minute)
	n, err := env.svc.ReconcileModelDefaultRecovery(ctx, pendingBefore)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("recovered %d, want 1", n)
	}
	if err := assertDefaultModelAuditPair(ctx, env.svc, staleRef); err != nil {
		t.Fatal(err)
	}

	// Second run: the same pending row is re-scanned but per-ref terminal guard
	// + finished claim must prevent a second terminal append → 0 compensations.
	rows, err := env.svc.Store.ListPendingModelDefaultAudits(ctx, pendingBefore)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 stale pending row, got %d", len(rows))
	}
	n2, err := env.svc.ReconcileModelDefaultRecovery(ctx, pendingBefore)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second run should be idempotent (0 compensations), got %d", n2)
	}
	if err := assertDefaultModelAuditPair(ctx, env.svc, staleRef); err != nil {
		t.Fatalf("second run invalidated the pair invariant: %v", err)
	}
}

// assertDefaultModelAuditPair asserts exactly two audit rows for the operation
// (one pending A + one terminal B) sharing the operation_ref.
func assertDefaultModelAuditPair(ctx context.Context, svc *Service, opRef string) error {
	rows, _, err := svc.Store.ListAuditsForScope(ctx, true, nil, 1, 1000, repository.AuditFilter{})
	if err != nil {
		return err
	}
	pending, terminal := 0, 0
	for _, row := range rows {
		if row.Action != "model-default.write" {
			continue
		}
		op, _, perr := parseDefaultModelOperation(row.DetailJSON)
		if perr != nil || op != opRef {
			continue
		}
		switch row.Result {
		case "pending":
			pending++
		case "succeeded", "failed", "unknown":
			terminal++
		}
	}
	if pending != 1 || terminal != 1 {
		return fmt.Errorf("audit pair op=%s: pending=%s terminal=%s", opRef, strconv.Itoa(pending), strconv.Itoa(terminal))
	}
	return nil
}

func TestModelDefaultListOptions(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	opts, err := env.svc.ListDefaultModelOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chat := opts["chat"]
	if len(chat) != 1 || chat[0].ModelID != env.chatModel || chat[0].Provider != "test" {
		t.Fatalf("chat options: %+v", chat)
	}
	emb := opts["embedding"]
	if len(emb) != 1 || emb[0].ModelID != env.embModel {
		t.Fatalf("embedding options: %+v", emb)
	}
	// Types without models come back empty (not absent keys count-wise).
	if len(opts["ocr"]) != 0 {
		t.Fatalf("ocr should be empty: %+v", opts["ocr"])
	}
}

func TestModelDefaultTransportFailWithoutLandingIsFailed(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	// Transport error AND the value never landed → 50256 with status failed
	// (not 50257): retrying is safe because nothing changed upstream.
	env.client.mu.Lock()
	env.client.setDefault = func(ctx context.Context, req ragflow.SetDefaultModelRequest) error {
		return errors.New("connection reset by peer")
	}
	env.client.mu.Unlock()

	res, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
	if defaultModelErrCode(err) != defaultModelErrSetTransport {
		t.Fatalf("want 50256, got err=%v (code=%d)", err, defaultModelErrCode(err))
	}
	// A failed-before-landing write returns no result at all: the caller must
	// never receive a half-filled write result it could mistake for success.
	if res != nil {
		t.Fatalf("failed write must not return a result: %+v", res)
	}

	// Row A stays "pending" forever (append-only hash chain: never updated), so
	// the invariant to check is that a terminal row B exists for the same
	// operation_ref — otherwise every failed write would become orphan evidence
	// that only the compensator could ever close.
	pending, err := env.svc.Store.ListPendingModelDefaultAudits(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) == 0 {
		t.Fatal("expected the pending row A to exist for the failed write")
	}
	for _, row := range pending {
		opRef, _, parseErr := parseDefaultModelOperation(row.DetailJSON)
		if parseErr != nil || opRef == "" {
			t.Fatalf("unparseable pending row: %+v", row)
		}
		hasTerminal, err := env.svc.Store.HasDefaultModelTerminalAudit(ctx, opRef)
		if err != nil {
			t.Fatal(err)
		}
		if !hasTerminal {
			t.Fatalf("pending row %s has no terminal row B (orphan audit)", opRef)
		}
	}
}

func TestModelDefaultReadFailureIsNotUnset(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	env.client.mu.Lock()
	env.client.listDefaults = func(ctx context.Context) ([]ragflow.DefaultModel, error) {
		return nil, errors.New("upstream list down")
	}
	env.client.mu.Unlock()

	// A read failure must surface as an error, never as eight "unset" rows
	// (doc/269 P0-5: 未设置 ≠ 读取失败).
	states, err := env.svc.GetTenantDefaultModels(ctx)
	if defaultModelErrCode(err) != defaultModelErrReadFailed {
		t.Fatalf("want 50293, got err=%v (code=%d) states=%+v", err, defaultModelErrCode(err), states)
	}
	if states != nil {
		t.Fatalf("read failure must not return rows: %+v", states)
	}

	// Options must fail the same way (no silent empty candidate map): it reads
	// the per-type model catalogue, so fail that probe instead.
	env.client.mu.Lock()
	env.client.listModels = func(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
		return nil, errors.New("upstream models down")
	}
	env.client.mu.Unlock()
	if _, err := env.svc.ListDefaultModelOptions(ctx); defaultModelErrCode(err) != defaultModelErrReadFailed {
		t.Fatalf("options: want 50293, got %v", err)
	}
}

// TestModelDefaultRecoveryReleasesWriteLock is a regression guard: the recovery
// compensator takes the platform write lock to read upstream out-of-band, and a
// discarded release function leaves the lock held — an in-process mutex
// fallback then deadlocks every later write, and the postgres advisory lock
// starves concurrent writers with 40913 until the session dies.
func TestModelDefaultRecoveryReleasesWriteLock(t *testing.T) {
	env := newDefaultModelEnv(t)
	ctx := context.Background()

	staleRef := "md:chat:stale-lock"
	if err := env.svc.appendDefaultModelAudit(ctx, env.tenantID, env.userID, "chat", staleRef, "pending", map[string]interface{}{
		"operation_ref":      staleRef,
		"requested_model_id": env.chatModel,
		"previous_model_id":  "",
		"model_type":         "chat",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.ReconcileModelDefaultRecovery(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("recovery run: %v", err)
	}

	// A normal write right after the compensator must not block.
	done := make(chan error, 1)
	go func() {
		_, err := env.svc.SetTenantDefaultModel(ctx, env.tenantID, env.userID, "chat", env.chatModel, expectUnset())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write after recovery: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write blocked: the recovery compensator leaked the model-default write lock")
	}
}
