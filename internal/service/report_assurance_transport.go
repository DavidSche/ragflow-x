package service

// Who answers the review question, and how we can tell (doc/304 E125/E127).
//
// The semantic layer used to have exactly one way to reach a model: POST the
// engine's `/api/v1/chat/completions`. On RAGFlow v1.0.0 that route **ignores the
// `model` field** — a body naming `does-not-exist-xyz` answers 200 with a correct
// Chinese paragraph, because the tenant's *default* chat model answers whatever the
// body says (probed 2026-11-24, doc/304 E125). The response carries no `model`
// field either, so from the caller's side the answerer is unobservable.
//
// That makes `review_model` and `independence` statements about *configuration*, not
// about who answered — the exact confusion `doc/301 §5.4` exists to prevent. Two
// consequences follow, and this file is both of them:
//
//  1. If the review reference resolves to a provider instance registered in
//     `rgx_model_provider_instance`, call that provider **directly** with an
//     OpenAI-compatible body. Then `model` selects, and the provider echoes the
//     model it used. The echo is not a courtesy: `Chat` refuses an answer whose
//     echo is missing or different, because "some model answered" is not evidence
//     that *this* model answered.
//  2. If it does not resolve — no instance, no registered model, no base URL — fall
//     back to the engine route, and downgrade the independence claim to `unknown`.
//     The run still produces verdicts (a tier-0 run is a real answer), but it no
//     longer claims a separation it cannot observe. `assuranceIndependenceFor` is
//     the only place that downgrade happens.
//
// What this does not do: it does not pick a review model. Which model *should*
// review is a PM/QA decision (doc/304 §7 item 15), and a code change must not
// smuggle one in as a default.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/strutil"
)

// The two transport names are stored in the run's frozen policy
// (`assurance_review_transport`) and in the `report.assurance_started` audit row,
// so a receipt says which path produced the verdicts. They are values, not prose,
// because a reader filters on them.
const (
	assuranceTransportProviderDirect = "provider_direct"
	assuranceTransportEngine         = "engine_chat"
	// assuranceTransportNone says "this run asked no model at all" — the tier_0 run
	// with no reviewer configured. It is a third value, not an empty one: an audit
	// row that leaves the field blank is indistinguishable from one that forgot to
	// record a decision (doc/304 E129).
	assuranceTransportNone = "none"

	// A review call is a long-form answer on a model that may be queued behind
	// other work. The engine client has its own long-form ceiling; this is the
	// direct path's equivalent, and it exists so a hung provider cannot pin a
	// worker forever.
	assuranceReviewCallTimeout = 2 * time.Minute
)

// assuranceReviewRoute is the resolved answer to "who will answer this run's
// review questions". It is frozen at trigger time (written into the run's policy)
// and re-resolved at execution time from the same rows; a divergence is logged and
// the execution-time route wins, because that is the one that actually gets called.
type assuranceReviewRoute struct {
	Transport string
	// Reason is why this transport was chosen, in the operator's vocabulary. It
	// reaches the log and the audit row; it never reaches a verdict.
	Reason    string
	Provider  string
	Instance  string
	Model     string
	BaseURL   string
	APIKeyEnc string
}

// Direct reports whether the route addresses a provider we can name.
func (r assuranceReviewRoute) Direct() bool {
	return r.Transport == assuranceTransportProviderDirect
}

// engineRoute is the fallback, and it always carries a reason: "why did this run
// not go direct" is the first question an operator asks when independence reads
// `unknown` (doc/304 E127).
func engineRoute(reason string) assuranceReviewRoute {
	return assuranceReviewRoute{Transport: assuranceTransportEngine, Reason: reason}
}

// assuranceReviewRouteOf is the decision, as a pure function over rows.
//
// It is pure on purpose: the rule "direct only when we can name the instance, the
// model and the URL" is the whole honesty of the layer, and it must be testable
// without a database, a network or a provider.
//
// Matching is by *name*, not by id: the review reference is operator-written
// (`model@instance@provider`), and ids in a settings value would be unreadable and
// stale after a re-registration.
func assuranceReviewRouteOf(
	ref ReportModelRef,
	instances []model.ModelProviderInstance,
	providers []model.ModelProvider,
	modelsByInstance map[string][]model.ModelProviderModel,
) assuranceReviewRoute {
	instanceName := strings.TrimSpace(ref.Instance)
	if instanceName == "" {
		// The engine has no notion of an instance: `ChatToModel` takes one, but on
		// v1.0.0 it only reaches the tenant default. Without an instance there is
		// nothing to address directly.
		return engineRoute("the review reference pins no provider instance")
	}
	modelName := strings.TrimSpace(ref.Name)
	if modelName == "" {
		modelName = strings.TrimSpace(ref.ModelID)
	}
	if modelName == "" {
		return engineRoute("the review reference pins no model name")
	}

	var instance *model.ModelProviderInstance
	for index := range instances {
		candidate := &instances[index]
		if candidate.InstanceName != instanceName {
			continue
		}
		if candidate.Status != "" && candidate.Status != model.ProviderStatusActive {
			return engineRoute("provider instance " + instanceName + " is " + candidate.Status)
		}
		instance = candidate
		break
	}
	if instance == nil {
		return engineRoute("no provider instance named " + instanceName + " is registered")
	}

	var provider *model.ModelProvider
	for index := range providers {
		if providers[index].ID == instance.ProviderID {
			provider = &providers[index]
			break
		}
	}
	providerName := ""
	if provider != nil {
		providerName = provider.Name
	}
	if ref.Provider != "" {
		// A reference that names a provider must match the instance's provider. A
		// mismatch is not a detail: it is the reference telling us it means a
		// different upstream than the one the instance points at.
		if providerName == "" || providerName != ref.Provider {
			return engineRoute("instance " + instanceName + " belongs to provider " +
				firstNonEmpty(providerName, "(unknown)") + ", not " + ref.Provider)
		}
	}

	registered := false
	for _, candidate := range modelsByInstance[instance.ID] {
		if candidate.ModelName != modelName {
			continue
		}
		if candidate.Status != "" && candidate.Status != model.ProviderStatusActive {
			return engineRoute("model " + modelName + " is " + candidate.Status + " on " + instanceName)
		}
		if candidate.ModelType != 0 && candidate.ModelType&model.ModelTypeChat == 0 {
			return engineRoute("model " + modelName + " is not a chat model on " + instanceName)
		}
		registered = true
		break
	}
	if !registered {
		// Calling an unregistered model name would put the provider in charge of
		// what "that model" means, which is the v1.0.0 failure mode this file
		// exists to avoid.
		return engineRoute("model " + modelName + " is not registered on instance " + instanceName)
	}

	baseURL := strings.TrimSpace(instance.BaseURL)
	apiKeyEnc := instance.APIKeyEnc
	if baseURL == "" && provider != nil {
		baseURL = strings.TrimSpace(provider.BaseURL)
		apiKeyEnc = provider.APIKeyEnc
	}
	if baseURL == "" {
		return engineRoute("instance " + instanceName + " has no base_url")
	}

	return assuranceReviewRoute{
		Transport: assuranceTransportProviderDirect,
		Reason:    "instance " + instanceName + " is registered with base_url; model " + modelName + " can be addressed directly",
		Provider:  providerName, Instance: instanceName, Model: modelName,
		BaseURL: baseURL, APIKeyEnc: apiKeyEnc,
	}
}

// resolveAssuranceReviewRoute loads the rows and applies the rule above.
//
// A nil store (unit tests, and any caller that builds a bare Service) resolves to
// the engine route rather than panicking: the fallback is a real code path, not an
// error case.
func (s *Service) resolveAssuranceReviewRoute(
	ctx context.Context, tenantID string, ref ReportModelRef,
) assuranceReviewRoute {
	if s.Store == nil {
		return engineRoute("the store is not configured in this process")
	}
	instances, err := s.Store.ListModelProviderInstancesByTenant(ctx, tenantID)
	if err != nil {
		return engineRoute("failed to list provider instances: " + err.Error())
	}
	providers, err := s.Store.ListModelProviders(ctx, tenantID)
	if err != nil {
		return engineRoute("failed to list providers: " + err.Error())
	}
	modelsByInstance := make(map[string][]model.ModelProviderModel, len(instances))
	for _, instance := range instances {
		rows, modelErr := s.Store.ListModelProviderModels(ctx, tenantID, instance.ID)
		if modelErr != nil {
			return engineRoute("failed to list models of instance " + instance.InstanceName + ": " + modelErr.Error())
		}
		modelsByInstance[instance.ID] = rows
	}
	return assuranceReviewRouteOf(ref, instances, providers, modelsByInstance)
}

// assuranceIndependenceFor is the only place an independence claim is allowed to
// weaken to `unknown`.
//
// The rule: a claim about *which* model answered is only carrying information when
// the answerer is observable. On the engine route it is not (v1.0.0 ignores
// `model` and echoes nothing), so a configured provider becomes `unknown` — the
// run reports that it could not tell, which is true, instead of naming a
// separation it cannot see (doc/304 E125/E127).
func assuranceIndependenceFor(configured string, route assuranceReviewRoute) string {
	if strings.TrimSpace(configured) == "" {
		return model.ReportIndependenceUnknown
	}
	if !route.Direct() {
		return model.ReportIndependenceUnknown
	}
	return configured
}

// assuranceReviewTransport is the one method the evaluator needs. Two
// implementations, because there are two ways a review question can be asked.
type assuranceReviewTransport interface {
	Chat(ctx context.Context, prompt string) (string, error)
	Name() string
}

// engineReviewTransport asks the RAGFlow engine. Kept as a transport (not deleted)
// because it is the only path that works when nothing is registered locally, and
// because a fallback that no longer compiles is a fallback that no longer works.
type engineReviewTransport struct {
	svc      *Service
	provider string
	instance string
	model    string
}

func (t *engineReviewTransport) Name() string { return assuranceTransportEngine }

func (t *engineReviewTransport) Chat(ctx context.Context, prompt string) (string, error) {
	return t.svc.RAGFlow.ChatToModel(ctx, t.provider, t.instance, t.model, prompt, false, false)
}

// directReviewTransport speaks OpenAI-compatible `/chat/completions` to the
// registered provider.
//
// It borrows `Service.httpClient`, so the direct path inherits the same network
// policy as every other provider call: dial-time host validation
// (`enterpriseProbeDialContext`, honouring `security.allow_private_provider_base_url`)
// and `providerRedirectPolicy` on redirects. Going direct is not a way around the
// SSRF guard — the base URL was accepted at registration and is re-checked here.
type directReviewTransport struct {
	client    *http.Client
	baseURL   string
	apiKeyEnc string
	encrypt   []byte
	model     string
}

func newDirectReviewTransport(svc *Service, route assuranceReviewRoute) *directReviewTransport {
	client := svc.httpClient
	if client == nil {
		client = &http.Client{Timeout: assuranceReviewCallTimeout}
	}
	return &directReviewTransport{
		client: client, baseURL: route.BaseURL, apiKeyEnc: route.APIKeyEnc,
		encrypt: svc.EncryptKey, model: route.Model,
	}
}

func (t *directReviewTransport) Name() string { return assuranceTransportProviderDirect }

// Chat sends one non-streaming completion and refuses any answer it cannot
// attribute to `t.model`.
//
// Three refusals, each a distinct failure an operator has to be able to see:
//   - HTTP error / unparsable body → the provider did not answer;
//   - empty content → it answered with nothing;
//   - echo missing or different → *some* model answered, and we cannot say which.
//
// The third is the one that makes this path trustworthy. The engine route cannot
// tell those apart, which is why it may not claim independence.
func (t *directReviewTransport) Chat(ctx context.Context, prompt string) (string, error) {
	apiKey := ""
	if t.apiKeyEnc != "" {
		plain, err := crypto.Decrypt(t.encrypt, t.apiKeyEnc)
		if err != nil {
			return "", assuranceReviewError("decrypt provider credential: " + err.Error())
		}
		apiKey = plain
	}
	body, err := json.Marshal(map[string]interface{}{
		"model":       t.model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0,
		"stream":      false,
		// vLLM-shaped servers (the recorded route is one, doc/environment §1.4.2)
		// otherwise spend the completion budget inside `reasoning` and return an
		// empty `content`. Servers that do not know the field ignore it.
		"chat_template_kwargs": map[string]interface{}{"enable_thinking": false},
	})
	if err != nil {
		return "", assuranceReviewError("encode provider request: " + err.Error())
	}
	target := strings.TrimRight(t.baseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return "", assuranceReviewError("build provider request: " + err.Error())
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return "", assuranceReviewError("provider request failed: " + err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", assuranceReviewError("read provider response: " + err.Error())
	}
	if response.StatusCode >= 300 {
		return "", assuranceReviewError("provider answered HTTP " + itoa(response.StatusCode) + ": " + truncateBodyForLog(raw))
	}
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", assuranceReviewError("provider response is not JSON: " + truncateBodyForLog(raw))
	}
	echoed := strings.TrimSpace(parsed.Model)
	if echoed == "" {
		return "", assuranceReviewError("provider did not echo the model it answered as; " +
			"an unattributable answer cannot be evidence that " + t.model + " answered")
	}
	if echoed != t.model {
		return "", assuranceReviewError("provider answered as " + echoed + ", not " + t.model)
	}
	text := ""
	for _, choice := range parsed.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			text = choice.Message.Content
			break
		}
		if strings.TrimSpace(choice.Text) != "" {
			text = choice.Text
			break
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", assuranceReviewError("provider " + t.model + " returned an empty answer")
	}
	return text, nil
}

// assuranceReviewError is a plain error: it reaches the run's `failure_reason`
// and the log, never an HTTP response, so it does not need an error code.
type assuranceReviewError string

func (e assuranceReviewError) Error() string { return string(e) }

// truncateBodyForLog keeps an error body useful without pasting a megabyte into a
// log line. (The live test file has its own string-shaped helper.)
func truncateBodyForLog(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) <= 300 {
		return text
	}
	// `strutil.Truncate` cuts on a rune boundary: a provider may answer in Chinese,
	// and half a rune in a log line is mojibake that costs more to read than the
	// extra line. (It is byte-bounded, so CJK bodies land near 100 characters —
	// enough to name the error, not enough to bury the line.)
	return strutil.Truncate(text, 300) + "…"
}

// assuranceTransportOf builds the transport a route names. It never returns nil:
// a route that cannot be built is the engine route's job to absorb.
func (s *Service) assuranceTransportOf(route assuranceReviewRoute, provider, instance, model string) assuranceReviewTransport {
	if route.Direct() {
		return newDirectReviewTransport(s, route)
	}
	return &engineReviewTransport{svc: s, provider: provider, instance: instance, model: model}
}

// logAssuranceReviewRoute records the route once per run, not per claim: a log line
// per claim would bury the one fact that matters under the claim text.
func logAssuranceReviewRoute(runID string, route assuranceReviewRoute, configured, effective string) {
	if configured == effective {
		logger.Info("report assurance review route", "run_id", runID,
			"transport", route.Transport, "model", route.Model, "independence", effective, "reason", route.Reason)
		return
	}
	logger.Warn("report assurance review independence downgraded", "run_id", runID,
		"transport", route.Transport, "configured", configured, "effective", effective, "reason", route.Reason)
}
