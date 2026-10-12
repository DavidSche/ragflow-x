package router_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/router"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type liveContextGovernanceApp struct {
	db      *gorm.DB
	server  *httptest.Server
	service *service.Service
}

type liveContextGovernanceFixture struct {
	nonce            string
	db               *gorm.DB
	app              *liveContextGovernanceApp
	client           *ragflow.HTTPClient
	tenantA          *model.Tenant
	tenantB          *model.Tenant
	owner            *model.User
	sameTenantMember *model.User
	otherTenantUser  *model.User
	policy           *model.MemoryPolicy
	chat             *model.ChatShadow
	session          *ragflow.Session
	memoryID         string
	query            string
	answer           string
	attribution      string
}

type contextDecisionEnvelope struct {
	Data model.ContextDecision `json:"data"`
}

type contextDecisionListEnvelope struct {
	Data struct {
		Items []model.ContextDecision `json:"items"`
	} `json:"data"`
}

func setupLiveContextGovernanceFixture(t *testing.T) *liveContextGovernanceFixture {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("RGX_TEST_POSTGRES_DSN"))
	liveBaseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	liveAPIKey := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"))
	if baseDSN == "" || liveBaseURL == "" || liveAPIKey == "" {
		t.Skip("RGX_TEST_POSTGRES_DSN, RGX_RAGFLOW_LIVE_BASE_URL and RGX_RAGFLOW_LIVE_API_KEY are required")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_DISPATCH_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_E2E_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1, RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1 and RGX_ALLOW_MEMORY_E2E_FIXTURE=1 are required")
	}

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping postgres admin: %v", err)
	}

	nonce := strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()))
	databaseName := "rgx_context_governance_" + nonce
	targetDSN := swapLiveGovernanceDatabase(baseDSN, databaseName)
	t.Cleanup(func() {
		if err := dropLiveGovernanceDatabase(admin, databaseName); err != nil {
			t.Errorf("drop disposable postgres database: %v", err)
		}
	})
	if err := dropLiveGovernanceDatabase(admin, databaseName); err != nil {
		t.Fatalf("clean disposable postgres database: %v", err)
	}
	if err := createLiveGovernanceDatabase(admin, databaseName); err != nil {
		t.Fatalf("create disposable postgres database: %v", err)
	}

	operational, err := gorm.Open(postgres.Open(targetDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open disposable postgres database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := operational.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.Migrate(operational); err != nil {
		t.Fatalf("migrate disposable postgres database: %v", err)
	}

	liveClient := ragflow.NewHTTPClient(liveBaseURL, liveAPIKey, 30*time.Second, 4)
	svc := service.New(repository.NewStore(operational), liveClient, jwt.NewManager("live-governance-secret", 24), "live-governance-secret")
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "live-governance-admin", "secret123"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	tenantA, err := svc.CreateTenant(ctx, "Context Governance A "+nonce)
	if err != nil {
		t.Fatalf("create tenant A: %v", err)
	}
	tenantB, err := svc.CreateTenant(ctx, "Context Governance B "+nonce)
	if err != nil {
		t.Fatalf("create tenant B: %v", err)
	}
	owner, err := svc.CreateUser(ctx, tenantA.ID, "", service.CreateUserRequest{
		Username: "ctx-owner-" + nonce, Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sameTenantMember, err := svc.CreateUser(ctx, tenantA.ID, "", service.CreateUserRequest{
		Username: "ctx-member-" + nonce, Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatalf("create same-tenant member: %v", err)
	}
	otherTenantUser, err := svc.CreateUser(ctx, tenantB.ID, "", service.CreateUserRequest{
		Username: "ctx-other-" + nonce, Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatalf("create cross-tenant user: %v", err)
	}

	chatModel := liveGovernanceModelID(t, liveClient, ctx, "chat")
	embeddingModel := liveGovernanceModelID(t, liveClient, ctx, "embedding")
	memoryName := "rgx_context_governance_" + nonce
	memoryID, err := liveClient.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: memoryName, MemoryType: []string{"raw"},
		EmbdID: embeddingModel, LLMID: chatModel,
	})
	if err != nil {
		t.Fatalf("create live memory: %v", err)
	}
	t.Cleanup(func() {
		if os.Getenv("RGX_KEEP_MEMORY_FIXTURE") == "1" {
			t.Logf("RGX_KEEP_MEMORY_FIXTURE=1; retaining memory %s", memoryID)
			return
		}
		if err := liveClient.DeleteMemory(context.Background(), memoryID); err != nil {
			t.Errorf("delete live memory: %v", err)
		}
	})

	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenantA.ID, UserID: owner.ID,
		AssistantID: "pending", RagflowMemoryID: memoryID, State: model.MemoryPolicyActive,
		ReadPolicy: "owner", WritePolicy: "owner", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: owner.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatalf("create memory policy: %v", err)
	}

	chat, err := svc.CreateChatWithConfig(ctx, tenantA.ID, "Context Governance Chat "+nonce, nil, service.ChatAuthoring{LLMID: chatModel})
	if err != nil {
		t.Fatalf("create live chat: %v", err)
	}
	t.Cleanup(func() {
		if err := svc.DeleteChat(context.Background(), tenantA.ID, chat.ID, false); err != nil {
			t.Errorf("delete live chat: %v", err)
		}
	})
	session, err := svc.CreateChatSession(ctx, tenantA.ID, chat.ID, "context governance", false)
	if err != nil {
		t.Fatalf("create live chat session: %v", err)
	}
	policy.AssistantID = chat.ID
	policy.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateMemoryPolicy(ctx, policy); err != nil {
		t.Fatalf("bind memory policy to chat: %v", err)
	}

	query := fmt.Sprintf("rgx live governance sentinel %s", nonce)
	answer := fmt.Sprintf("The live governance sentinel is %s.", nonce)
	attribution := "rgx:" + tenantA.ID + ":" + owner.ID
	if err := liveClient.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: chat.ID, SessionID: session.ID,
		UserID: attribution, UserInput: query, AgentResponse: answer,
	}); err != nil {
		t.Fatalf("add live memory message: %v", err)
	}
	waitForLiveGovernanceMemory(t, liveClient, ctx, memoryID, chat.ID, session.ID, attribution, query)

	cfg := config.Config{
		Server: config.Server{Mode: "release"},
		App: config.App{
			JWTSecret:       "live-governance-secret",
			JWTExpireHours:  24,
			RequestIDHeader: "X-Request-Id",
		},
	}
	handlerInstance := handler.New(svc)
	engine := router.New(cfg, handlerInstance, ratelimit.NewMemory())
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	return &liveContextGovernanceFixture{
		nonce: nonce, db: operational,
		app:    &liveContextGovernanceApp{db: operational, server: server, service: svc},
		client: liveClient, tenantA: &tenantA, tenantB: &tenantB,
		owner: owner, sameTenantMember: sameTenantMember, otherTenantUser: otherTenantUser,
		policy: policy, chat: chat, session: session, memoryID: memoryID,
		query: query, answer: answer, attribution: attribution,
	}
}

func TestLiveProductionBackendContextGovernanceMultiTenant(t *testing.T) {
	fixture := setupLiveContextGovernanceFixture(t)
	app := fixture.app
	requestID := "live-governance-" + fixture.nonce
	chatBody := fmt.Sprintf(
		`{"chat_id":%q,"session_id":%q,"messages":[{"role":"user","content":%q}]}`,
		fixture.chat.ID, fixture.session.ID, fixture.query,
	)

	ownerToken := app.login(t, fixture.owner.Username, "secret123")
	resp, body := app.post(t, ownerToken, "/api/v1/chat/completions", chatBody, requestID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("governed chat completion: status=%d body=%s", resp.StatusCode, body)
	}

	decisionPath := "/api/v1/conversation-context/decisions/by-request/" + url.PathEscape(requestID)
	ownerResp, ownerBody := app.get(t, ownerToken, decisionPath)
	if ownerResp.StatusCode != http.StatusOK {
		t.Fatalf("owner decision read: status=%d body=%s", ownerResp.StatusCode, ownerBody)
	}
	var ownerDecision contextDecisionEnvelope
	if err := json.Unmarshal(ownerBody, &ownerDecision); err != nil {
		t.Fatalf("decode owner decision: %v", err)
	}
	if ownerDecision.Data.RequestID != requestID || ownerDecision.Data.UserID != fixture.owner.ID ||
		ownerDecision.Data.TenantID != fixture.tenantA.ID || ownerDecision.Data.Decision != "memory_allowed" ||
		ownerDecision.Data.MemoryPolicyID != fixture.policy.ID {
		t.Fatalf("unexpected owner decision: %+v", ownerDecision.Data)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(ownerDecision.Data.Detail), &detail); err != nil {
		t.Fatalf("decode owner decision detail: %v", err)
	}
	if detail["memory_retrieved"] != true || detail["memory_result_count"].(float64) < 1 {
		t.Fatalf("decision does not prove governed retrieval: %+v", detail)
	}

	directPath := "/api/v1/conversation-context/decisions/" + url.PathEscape(ownerDecision.Data.ID)
	directResp, directBody := app.get(t, ownerToken, directPath)
	if directResp.StatusCode != http.StatusOK {
		t.Fatalf("owner direct decision read: status=%d body=%s", directResp.StatusCode, directBody)
	}
	assertNoSensitiveLeakage(t, "owner", directBody, []string{fixture.query, fixture.answer})

	memberToken := app.login(t, fixture.sameTenantMember.Username, "secret123")
	otherToken := app.login(t, fixture.otherTenantUser.Username, "secret123")
	for _, actor := range []struct {
		name  string
		token string
	}{{"same tenant non-owner", memberToken}, {"cross tenant user", otherToken}} {
		resp, body = app.get(t, actor.token, decisionPath)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s by-request read: status=%d body=%s", actor.name, resp.StatusCode, body)
		}
		resp, body = app.get(t, actor.token, directPath)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s direct read: status=%d body=%s", actor.name, resp.StatusCode, body)
		}
		assertNoSensitiveLeakage(t, actor.name, body, []string{
			fixture.query, fixture.answer, fixture.memoryID, fixture.policy.ID, ownerDecision.Data.ID,
		})
	}

	memberListResp, memberListBody := app.get(t, memberToken, "/api/v1/conversation-context/decisions")
	if memberListResp.StatusCode != http.StatusOK {
		t.Fatalf("same tenant list: status=%d body=%s", memberListResp.StatusCode, memberListBody)
	}
	var memberList contextDecisionListEnvelope
	if err := json.Unmarshal(memberListBody, &memberList); err != nil {
		t.Fatalf("decode same tenant decision list: %v", err)
	}
	for _, item := range memberList.Data.Items {
		if item.ID == ownerDecision.Data.ID || item.RequestID == requestID {
			t.Fatalf("same tenant list exposed owner decision: %+v", item)
		}
	}
}

func TestLiveProductionBackendContextGovernanceBrowser(t *testing.T) {
	if os.Getenv("RGX_LIVE_BROWSER_E2E") != "1" {
		t.Skip("RGX_LIVE_BROWSER_E2E=1 is required")
	}
	fixture := setupLiveContextGovernanceFixture(t)

	environment := append(os.Environ(),
		"RGX_LIVE_E2E=1",
		"RGX_API_PROXY_TARGET="+fixture.app.server.URL,
		"RGX_E2E_OWNER_USERNAME="+fixture.owner.Username,
		"RGX_E2E_OWNER_PASSWORD=secret123",
		"RGX_E2E_MEMBER_USERNAME="+fixture.sameTenantMember.Username,
		"RGX_E2E_MEMBER_PASSWORD=secret123",
		"RGX_E2E_OTHER_USERNAME="+fixture.otherTenantUser.Username,
		"RGX_E2E_OTHER_PASSWORD=secret123",
		"RGX_E2E_CHAT_ID="+fixture.chat.ID,
		"RGX_E2E_SESSION_ID="+fixture.session.ID,
		"RGX_E2E_QUERY="+fixture.query,
		"RGX_E2E_ANSWER="+fixture.answer,
		"RGX_E2E_MEMORY_ID="+fixture.memoryID,
		"RGX_E2E_POLICY_ID="+fixture.policy.ID,
	)
	browserTest := exec.Command(
		"npx", "playwright", "test",
		"context-source-production-backend.spec.ts",
		"--project=chromium", "--workers=1", "--retries=0",
	)
	browserTest.Dir = filepath.Join("..", "..", "web")
	browserTest.Env = environment
	browserTest.Stdout = os.Stdout
	browserTest.Stderr = os.Stderr
	if err := browserTest.Run(); err != nil {
		t.Fatalf("production backend browser governance drill: %v", err)
	}
}

func (app *liveContextGovernanceApp) login(t *testing.T, username, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(app.server.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status=%d body=%s", username, resp.StatusCode, body)
	}
	var envelope struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.Token == "" {
		t.Fatalf("decode login response: err=%v body=%s", err, body)
	}
	return envelope.Data.Token
}

func (app *liveContextGovernanceApp) post(t *testing.T, token, path, payload, requestID string) (*http.Response, []byte) {
	t.Helper()
	return app.request(t, http.MethodPost, token, path, payload, requestID)
}

func (app *liveContextGovernanceApp) get(t *testing.T, token, path string) (*http.Response, []byte) {
	t.Helper()
	return app.request(t, http.MethodGet, token, path, "", "")
}

func (app *liveContextGovernanceApp) request(t *testing.T, method, token, path, payload, requestID string) (*http.Response, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var reader io.Reader
	if payload != "" {
		reader = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, app.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response %s %s: %v", method, path, err)
	}
	return resp, body
}

func assertNoSensitiveLeakage(t *testing.T, actor string, body []byte, sensitive []string) {
	t.Helper()
	encoded := string(body)
	for _, value := range sensitive {
		if value != "" && strings.Contains(encoded, value) {
			t.Fatalf("%s response leaked sensitive value", actor)
		}
	}
}

func waitForLiveGovernanceMemory(
	t *testing.T, client *ragflow.HTTPClient, ctx context.Context,
	memoryID, assistantID, sessionID, attribution, query string,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		rows, err := client.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{
			Query: query, AgentID: assistantID, SessionID: sessionID,
			UserID: attribution, TopN: 5,
		})
		if err == nil {
			for _, row := range rows {
				if row.AgentID == assistantID && row.SessionID == sessionID && row.UserID == attribution {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live memory fixture stopped before search: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("live memory fixture did not become searchable")
}

func liveGovernanceModelID(t *testing.T, client *ragflow.HTTPClient, ctx context.Context, modelType string) string {
	t.Helper()
	models, err := client.ListModels(ctx, modelType)
	if err != nil {
		t.Fatalf("list live %s models: %v", modelType, err)
	}
	for _, candidate := range models {
		if strings.TrimSpace(candidate.Name) == "" || strings.TrimSpace(candidate.Provider) == "" {
			continue
		}
		modelID := candidate.Name + "@" + candidate.Provider
		if strings.TrimSpace(candidate.Instance) != "" {
			modelID = candidate.Name + "@" + candidate.Instance + "@" + candidate.Provider
		}
		return modelID
	}
	t.Skipf("live governance drill requires an enabled %s model", modelType)
	return ""
}

func swapLiveGovernanceDatabase(dsn, name string) string {
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" && strings.Contains(parsed.Scheme, "postgres") {
		parsed.Path = "/" + name
		return parsed.String()
	}
	fields := strings.Fields(dsn)
	out := make([]string, 0, len(fields)+1)
	replaced := false
	for _, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "dbname=") {
			out = append(out, "dbname="+name)
			replaced = true
			continue
		}
		out = append(out, field)
	}
	if !replaced {
		out = append(out, "dbname="+name)
	}
	return strings.Join(out, " ")
}

func createLiveGovernanceDatabase(admin *sql.DB, name string) error {
	if !validLiveGovernanceDatabaseName(name) {
		return fmt.Errorf("invalid postgres database name")
	}
	_, err := admin.Exec("CREATE DATABASE " + name)
	return err
}

func dropLiveGovernanceDatabase(admin *sql.DB, name string) error {
	if !validLiveGovernanceDatabaseName(name) {
		return fmt.Errorf("invalid postgres database name")
	}
	_, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	return err
}

func validLiveGovernanceDatabaseName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}
		return false
	}
	return true
}
