package service

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// The settings registry has three registration points (doc/319 §5.3 S-4,
// doc/301 §7.6): the *policy* (may this be changed, how risky, how does it take
// effect), the *validator* (what is a legal value), and the *default snapshot*
// (what a tenant reads before anyone edits it). A key missing from any one of the
// three produces a different bug, and all three bugs look like something else:
//
//   - policy without validator   → the panel says editable, the write answers
//     "unsupported system setting" (40098);
//   - validator without policy   → the key validates, then the policy lookup
//     refuses the write; dead code with a passing unit test;
//   - no default                 → the tenant reads a zero value. For
//     `candidate_scan_top_k` zero already means "off", so "never turned on" and
//     "configured wrong" become the same picture.
//
// The gate below is deliberately about *agreement between registries*, not about
// values: value ranges already have their own test.
func TestSettingRegistriesAgree(t *testing.T) {
	var problems []string

	// (1) A policy that can be changed through the settings API must be validated.
	for key, policy := range settingKeyPolicies {
		if !policy.Editable || !policy.DatabaseOverride {
			continue
		}
		metadata, ok := settingKeyValidators[key]
		if !ok {
			problems = append(problems, key+": editable+database_override but no validator, so a write is refused as unsupported")
			continue
		}
		if key != metadata.Group+"."+metadata.Field {
			problems = append(problems, fmt.Sprintf("%s: validator names %s.%s", key, metadata.Group, metadata.Field))
		}
		if metadata.Type == "" {
			problems = append(problems, key+": validator has no type, so any JSON value is accepted")
		}
	}

	// (2) A validator without a policy is unreachable: the policy lookup happens
	// first and refuses the write as "unsupported system setting".
	for key := range settingKeyValidators {
		if _, ok := settingKeyPolicies[key]; !ok {
			problems = append(problems, key+": validator without policy, so no request can ever reach it")
		}
	}

	// (3) Every default leaf is a policy key, and every policy that the panel can
	// show has a default. A policy with no default and no edit right is a key read
	// straight from deployment config (server.data_dir, server.log_path) — those are
	// the only shape allowed to skip the snapshot, and the rule is derived from the
	// policy itself rather than from a list of exceptions.
	leaves := map[string]bool{}
	flattenSettingLeaves("", defaultSystemSettingsSnapshot()["groups"], leaves)
	for leaf := range leaves {
		if _, ok := settingKeyPolicies[leaf]; !ok {
			problems = append(problems, leaf+": default value without a policy, so the panel omits a value it renders")
		}
	}
	for key, policy := range settingKeyPolicies {
		if leaves[key] {
			continue
		}
		if policy.Editable || policy.DatabaseOverride {
			problems = append(problems, key+": settable but absent from the default snapshot, so the tenant reads a zero value")
		}
	}

	// (4) The two descriptive fields are part of the contract: the panel shows
	// `impact` next to the confirm button and `effective_mode` next to the value.
	// An editable, database-overridable key with an empty impact asks the operator
	// to confirm a change whose effect is not stated.
	sourceModes := map[string]bool{"yaml_env": true, "yaml_env_db": true}
	effectiveModes := map[string]bool{"hot_reload": true, "pending_restart": true, "deployment_controlled": true}
	riskLevels := map[string]bool{"low": true, "medium": true, "high": true}
	for key, policy := range settingKeyPolicies {
		if !sourceModes[policy.Source] {
			problems = append(problems, key+": unknown source "+policy.Source)
		}
		if !effectiveModes[policy.EffectiveMode] {
			problems = append(problems, key+": unknown effective_mode "+policy.EffectiveMode+` (the vocabulary is hot_reload / pending_restart / deployment_controlled)`)
		}
		if !riskLevels[policy.RiskLevel] {
			problems = append(problems, key+": unknown risk_level "+policy.RiskLevel)
		}
		if policy.Editable && policy.DatabaseOverride && strings.TrimSpace(policy.Impact) == "" {
			problems = append(problems, key+": editable but impact is empty, so the panel confirms a change it cannot describe")
		}
		// The two fields must not disagree. `restart_required: false` with
		// `effective_mode: pending_restart` would tell the panel both "no restart
		// needed" and "takes effect after restart" on the same row.
		if policy.RestartRequired && policy.EffectiveMode == "hot_reload" {
			problems = append(problems, key+": restart_required but effective_mode is hot_reload")
		}
		if !policy.RestartRequired && policy.EffectiveMode == "pending_restart" {
			problems = append(problems, key+": effective_mode is pending_restart but restart_required is false")
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("settings registries disagree:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestGatewaySettingsReachTheRuntimeClients is the behavioural half of the same
// seam: `effective_mode: hot_reload` is a claim that a saved value reaches the
// component that reads it without a restart. For the four hot gateway keys that
// component is the provider HTTP clients, rebuilt by `ApplySystemSettingsSnapshot`
// through `SetGatewayClients`. The assertion is on the built client's timeouts —
// not on the snapshot — because the snapshot is the input, and "the input was
// stored" is exactly the sort of evidence that stays green while nothing happens.
func TestGatewaySettingsReachTheRuntimeClients(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.ApplySystemSettingsSnapshot(map[string]interface{}{}) // establish the defaults
	baseline := svc.httpClient
	if baseline == nil {
		t.Fatal("no provider client built from the default snapshot")
	}

	snapshot := defaultSystemSettingsSnapshot()
	gateway := snapshot["groups"].(map[string]interface{})["gateway"].(map[string]interface{})
	gateway["non_stream_timeout_sec"] = 7
	gateway["stream_header_timeout_sec"] = 9
	gateway["stream_total_timeout_sec"] = 11
	gateway["max_conns"] = 3
	if err := svc.ApplySystemSettingsSnapshot(snapshot); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if svc.httpClient == baseline {
		t.Fatal("the provider client was not rebuilt: `hot_reload` is false for the gateway group")
	}
	if got := svc.httpClient.Timeout; got != 7*time.Second {
		t.Fatalf("non_stream_timeout_sec did not reach the client: %v", got)
	}
	if got := svc.streamClient.Timeout; got != 11*time.Second {
		t.Fatalf("stream_total_timeout_sec did not reach the stream client: %v", got)
	}
	transport, ok := svc.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("provider client transport is %T", svc.httpClient.Transport)
	}
	if got := transport.ResponseHeaderTimeout; got != 9*time.Second {
		t.Fatalf("stream_header_timeout_sec did not reach the transport: %v", got)
	}
	if got := transport.MaxConnsPerHost; got != 3 {
		t.Fatalf("max_conns did not reach the transport: %d", got)
	}
}

// TestRateLimitOverrideReachesTheStartupConfig covers the one `pending_restart`
// key in the gateway group. The limiter is constructed from `cfg.Gateway` while the
// router is assembled, so the only place a database value can enter is the
// effective-config pass at startup; before this test existed, nothing read
// `gateway.rate_limit_per_minute` from the snapshot anywhere, and the policy
// advertised it as a live, database-overridable knob.
func TestRateLimitOverrideReachesTheStartupConfig(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := t.Context()
	base := config.Config{}
	base.Gateway.RateLimitPerMin = 120
	if err := svc.InitializeSystemSettingsFromConfig(ctx, base); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// The bootstrap snapshot carries the deployment's value; the effective pass
	// must reproduce it rather than fall back to a literal.
	untouched, err := svc.EffectiveSystemSettingsConfig(ctx, base)
	if err != nil {
		t.Fatalf("effective config (untouched): %v", err)
	}
	if untouched.Gateway.RateLimitPerMin != 120 {
		t.Fatalf("an untouched deployment changed rate limit from 120 to %d",
			untouched.Gateway.RateLimitPerMin)
	}

	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil || current == nil {
		t.Fatalf("current revision: %+v err=%v", current, err)
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: current.Revision,
		Changes:          map[string]interface{}{"gateway.rate_limit_per_minute": 42},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	effective, err := svc.EffectiveSystemSettingsConfig(ctx, base)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if effective.Gateway.RateLimitPerMin != 42 {
		t.Fatalf("gateway.rate_limit_per_minute = %d, want 42 (the router would build its limiter with the yaml value)",
			effective.Gateway.RateLimitPerMin)
	}
}

func flattenSettingLeaves(prefix string, value interface{}, out map[string]bool) {
	nested, ok := value.(map[string]interface{})
	if !ok {
		if prefix != "" {
			out[strings.TrimPrefix(prefix, ".")] = true
		}
		return
	}
	for field, child := range nested {
		flattenSettingLeaves(prefix+"."+field, child, out)
	}
}
