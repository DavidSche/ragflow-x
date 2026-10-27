package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"time"

	"gorm.io/gorm"

	"github.com/gin-gonic/gin"

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

// RunnerOptions controls the fixed dimensions of a local baseline run. The
// upstream is always controlled in-process; production model latency is not
// represented by these numbers.
type RunnerOptions struct {
	Config         *config.Config
	Version        string
	GitRevision    string
	CPUModel       string
	MemoryGB       string
	DataScale      string
	KnowledgeBases int
}

// App owns the in-process stack and scenario credentials.
type App struct {
	providerServer *httptest.Server
	runner         *service.Runner
	db             *gorm.DB
	server         *httptest.Server
	service        *service.Service
	store          repository.Store
	mock           *ragflow.Mock
	providerClient ragflow.Client
	limiter        ratelimit.Limiter
	key            *model.APIKey
	keySecret      string
	quotaKey       *model.APIKey
	user           *model.User
	tenantID       string
	operatorName   string
	workerKind     string
	chatID         string
	sessionID      string
	gatewayBody    string
	chatBody       string
}

// NewApp builds a complete in-process application using the supplied database
// configuration. It always uses the controlled RAGFlow Mock.
func NewApp(ctx context.Context, options RunnerOptions) (*App, error) {
	if options.Config == nil {
		return nil, fmt.Errorf("performance: config is required")
	}
	cfg := *options.Config
	cfg.RAGFlow = config.RAGFlow{Provider: "mock", Timeout: 15, MaxConns: 64}
	cfg.Security.AllowPrivateProviderBaseURL = true
	cfg.Gateway.RateLimitPerMin = 0
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "release"
	}
	gin.SetMode(gin.ReleaseMode)

	gdb, err := db.Open(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("performance: open database: %w", err)
	}
	if err := db.Migrate(gdb); err != nil {
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: migrate database: %w", err)
	}
	store := repository.NewStore(gdb)
	mock := ragflow.NewMock()
	jwtManager := jwt.NewManager(cfg.App.JWTSecret, 24)
	svc := service.New(store, mock, jwtManager, cfg.App.JWTSecret)
	svc.SetProviderURLPolicy(true)
	if err := svc.BootstrapAdmin(ctx, "perf-admin", "perf-admin-password"); err != nil {
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: bootstrap admin: %w", err)
	}
	suffix := id.New()[:8]
	tenant, err := svc.CreateTenant(ctx, "perf-baseline-"+suffix)
	if err != nil {
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create tenant: %w", err)
	}
	operatorName := "perf-operator-" + suffix
	user, err := svc.CreateUser(ctx, tenant.ID, model.RolePlatformAdmin, service.CreateUserRequest{
		Username: operatorName, Password: "perf-operator-password", Role: model.RolePlatformAdmin,
	})
	if err != nil {
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create user: %w", err)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" && r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(time.Millisecond)
		var body struct {
			Stream bool `json:"stream"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for index := 0; index < 8; index++ {
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"perf-stream\",\"choices\":[{\"delta\":{\"content\":\"chunk-%d\"}}]}\n\n", index)
				if flusher != nil {
					flusher.Flush()
				}
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		response := map[string]interface{}{
			"id": "perf-completion", "model": "perf-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "perf response"}}},
			"usage":   map[string]int64{"prompt_tokens": 12, "completion_tokens": 8},
		}
		if r.URL.Path == "/api/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": response})
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	providerClient := ragflow.NewHTTPClient(providerServer.URL, "perf-provider-key", 15*time.Second, 64)

	provider, err := svc.CreateModelProvider(ctx, tenant.ID, service.CreateModelProviderRequest{
		Name: "perf-provider", ProviderType: "openai", BaseURL: providerServer.URL,
		APIKey: "perf-provider-key", Enabled: true, ModelName: "perf-target", Register: boolPtr(false),
	})
	if err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create model provider: %w", err)
	}
	if _, err = svc.CreateModelRoute(ctx, tenant.ID, service.CreateModelRouteRequest{
		ProviderID: provider.ID, ModelAlias: "perf-model", TargetModel: "perf-target", Scenario: "chat",
	}); err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create model route: %w", err)
	}
	secret, apiKey, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "perf-key-"+suffix, nil, 0, 0, nil, nil)
	if err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create api key: %w", err)
	}
	_, quotaKey, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "perf-quota-key-"+suffix, nil, 1_000_000_000, 1_000_000_000, nil, nil)
	if err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create quota api key: %w", err)
	}
	quotaPeriod := time.Now().UTC().Format("2006-01")
	if err := store.UpsertQuotaLimit(ctx, &model.QuotaLimit{
		TenantID: tenant.ID, KeyID: quotaKey.ID, PeriodStart: quotaPeriod,
		TokenLimit: quotaKey.TokenQuota, RequestLimit: quotaKey.RequestQuota,
	}); err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: seed quota limit: %w", err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "perf-chat-"+suffix, nil)
	if err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create chat: %w", err)
	}
	session, err := svc.CreateChatSession(ctx, tenant.ID, chat.ID, "perf-session-"+suffix, false)
	if err != nil {
		providerServer.Close()
		closeGorm(gdb)
		return nil, fmt.Errorf("performance: create chat session: %w", err)
	}

	limiter := ratelimit.NewMemory()
	const workerKind = "performance_single_task"
	workerConfig := service.DefaultWorkerConfig()
	workerConfig.PollInterval = 10 * time.Millisecond
	workerConfig.BatchSize = 64
	workerConfig.HeartbeatInterval = 10 * time.Millisecond
	workerConfig.HeartbeatTimeout = time.Second
	workerConfig.RequeueInterval = 50 * time.Millisecond
	workerConfig.JobTimeout = 5 * time.Second
	workerConfig.BaseBackoff = 10 * time.Millisecond
	workerConfig.MaxBackoff = 100 * time.Millisecond
	runner := svc.SetupWorker(workerConfig)
	runner.Register(performanceWorker{})
	runner.Start(context.Background())
	engine := router.New(cfg, handler.New(svc), limiter)
	server := httptest.NewServer(engine)
	return &App{
		providerServer: providerServer, runner: runner, db: gdb, server: server, service: svc,
		store: store, mock: mock, providerClient: providerClient, limiter: limiter,
		key: apiKey, keySecret: secret, quotaKey: quotaKey, user: user,
		tenantID: tenant.ID, operatorName: operatorName, workerKind: workerKind,
		chatID: chat.ID, sessionID: session.ID,
		gatewayBody: `{"model":"perf-model","messages":[{"role":"user","content":"performance baseline"}],"max_tokens":16}`,
		chatBody:    fmt.Sprintf(`{"chat_id":%q,"session_id":%q,"messages":[{"role":"user","content":"performance baseline"}]}`, chat.ID, session.ID),
	}, nil
}

// BaseURL returns the in-process HTTP base URL.
func (a *App) BaseURL() string { return a.server.URL }

// Scenarios returns the seven frozen minimum workloads.
func (a *App) Scenarios() []Scenario {
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64}}
	auth := map[string]string{"Authorization": "Bearer " + a.keySecret}
	gatewayProbe := GenericJSONProbe(http.MethodPost, a.server.URL+"/v1/chat/completions", a.gatewayBody, http.StatusOK, "perf response")
	gatewayProbe.Headers = auth
	streamBody := `{"model":"perf-model","stream":true,"messages":[{"role":"user","content":"performance baseline"}],"max_tokens":16}`
	streamProbe := HTTPProbe{URL: a.server.URL + "/v1/chat/completions", Method: http.MethodPost, Headers: auth, Body: streamBody, ExpectedCode: http.StatusOK, Stream: true}

	return []Scenario{
		{
			Name: "gateway_non_stream", Kind: "HTTP + Provider",
			Description: "OpenAI-compatible /v1/chat/completions with API key authentication, model routing, usage metering and controlled upstream.",
			Operation:   HTTPOperation(client, gatewayProbe),
		},
		{
			Name: "gateway_sse", Kind: "HTTP SSE",
			Description: "Full gateway SSE response; TTFT is measured before the first streamed byte and the run drains through data: [DONE].",
			Operation:   HTTPOperation(client, streamProbe),
		},
		{
			Name: "qa_ragflow_chat", Kind: "Q&A / RAGFlow Chat",
			Description: "Non-streaming RAGFlow chat assistant path through owned chat/session authorization and usage metering.",
			Operation:   a.ragflowChatOperation(),
		},
		{
			Name: "ragflow_provider_roundtrip", Kind: "Controlled Provider",
			Description: "Provider client roundtrip without HTTP gateway, quota or API-key middleware; it isolates the RAGFlow client boundary.",
			Operation:   a.providerOperation(),
		},
		{
			Name: "postgresql_core_api", Kind: "PostgreSQL",
			Description: "Concurrent core API identity lookup used by authenticated request paths.",
			Operation:   a.postgresOperation(),
		},
		{
			Name: "rate_limit_and_quota", Kind: "Rate Limit + Quota Read",
			Description: "Sliding-window limiter increment and concurrent quota-limit read; atomic reservation semantics remain covered by dedicated quota tests.",
			Operation:   a.quotaOperation(),
		},
		{
			Name: "worker_single_task", Kind: "Evaluation Worker",
			Description: "Durable job enqueue, atomic claim and settled success for one no-op worker task.",
			Operation:   a.workerOperation(),
		},
	}
}

func (a *App) ragflowChatOperation() Operation {
	return func(ctx context.Context) (Attempt, error) {
		_, err := a.service.ChatAppCompletion(ctx, a.key, a.chatID, a.sessionID, []byte(a.chatBody), a.requestID())
		return Attempt{}, err
	}
}

func (a *App) providerOperation() Operation {
	return func(ctx context.Context) (Attempt, error) {
		resp, err := a.providerClient.ChatCompletion(ctx, a.chatID, ragflow.CompletionRequest{
			ChatID: a.chatID, SessionID: a.sessionID, Stream: false,
		})
		if err != nil {
			return Attempt{}, err
		}
		if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
			return Attempt{}, fmt.Errorf("invalid provider response")
		}
		return Attempt{}, nil
	}
}

func (a *App) postgresOperation() Operation {
	return func(ctx context.Context) (Attempt, error) {
		user, err := a.store.GetUserByUsername(ctx, a.operatorName)
		if err != nil {
			return Attempt{}, err
		}
		if user == nil || user.ID == "" {
			return Attempt{}, fmt.Errorf("postgres lookup returned no user")
		}
		return Attempt{}, nil
	}
}

func (a *App) workerOperation() Operation {
	return func(ctx context.Context) (Attempt, error) {
		key := "performance:" + a.requestID()
		inserted, err := a.runner.Enqueue(ctx, a.workerKind, key, a.tenantID, "{}", time.Time{}, 1)
		if err != nil {
			return Attempt{}, err
		}
		if !inserted {
			return Attempt{}, fmt.Errorf("performance job key collision")
		}
		for ctx.Err() == nil {
			jobs, _, err := a.store.ListJobs(ctx, a.tenantID, a.workerKind, model.JobStatusSucceeded, 1, 256)
			if err != nil {
				return Attempt{}, err
			}
			for _, job := range jobs {
				if job.Key == key {
					return Attempt{}, nil
				}
			}
			time.Sleep(time.Millisecond)
		}
		return Attempt{}, ctx.Err()
	}
}

func (a *App) quotaOperation() Operation {
	return func(ctx context.Context) (Attempt, error) {
		if _, err := a.limiter.Incr(ctx, "performance:gateway", time.Minute); err != nil {
			return Attempt{}, err
		}
		quota, err := a.store.GetQuotaLimit(ctx, a.quotaKey.ID, time.Now().UTC().Format("2006-01"))
		if err != nil {
			return Attempt{}, err
		}
		if quota == nil || quota.TokenLimit <= 0 {
			return Attempt{}, fmt.Errorf("quota limit seed missing")
		}
		return Attempt{}, nil
	}
}

func (a *App) requestID() string {
	return "perf-" + id.New()
}

// Close releases HTTP, DB and limiter resources.
func (a *App) Close() {
	if a.server != nil {
		a.server.Close()
	}
	if a.providerServer != nil {
		a.providerServer.Close()
	}
	if a.runner != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.runner.Stop(stopCtx)
		cancel()
	}
	if a.limiter != nil {
		_ = a.limiter.Close()
	}
	if a.db != nil {
		closeGorm(a.db)
	}
}

func closeGorm(gdb *gorm.DB) {
	if gdb == nil {
		return
	}
	if sqlDB, err := gdb.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// RunApp runs the frozen scenarios and returns the report.
func RunApp(ctx context.Context, options RunnerOptions, sampler *Sampler) (*Report, error) {
	app, err := NewApp(ctx, options)
	if err != nil {
		return nil, err
	}
	defer app.Close()
	environment := Environment{
		Host: hostName(), OS: runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		CPUModel: options.CPUModel, MemoryGB: options.MemoryGB,
		Database:  options.Config.Database.Driver,
		Upstream:  "ragflow Mock / controlled HTTP upstream",
		DataScale: options.DataScale, KnowledgeBases: options.KnowledgeBases,
		GitRevision: options.GitRevision,
	}
	if sampler == nil {
		sampler = NewSampler(nil)
	}
	report, err := sampler.Run(ctx, app.Scenarios(), environment)
	if err != nil {
		return nil, err
	}
	report.Version = options.Version
	return report, nil
}

func hostName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

func boolPtr(value bool) *bool { return &value }

type performanceWorker struct{}

func (performanceWorker) Kind() string { return "performance_single_task" }

func (performanceWorker) Run(_ context.Context, job *model.Job) error {
	if job == nil || job.Payload != "{}" {
		return fmt.Errorf("invalid performance job payload")
	}
	return nil
}
