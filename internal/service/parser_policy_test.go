package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newParserPolicyTestService(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "parser-policy.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func qualityMetric(weight float64) QualityMetric {
	return QualityMetric{Weight: weight, Enabled: true}
}

func qualityProfileInput(name string) QualityProfileInput {
	active := true
	return QualityProfileInput{
		Name:           name,
		Metrics:        map[string]QualityMetric{"text_health": qualityMetric(1)},
		Thresholds:     map[string]float64{},
		EvaluationMode: model.QualityEvaluationHeuristicOnly,
		Active:         &active,
	}
}

func parserPolicyInput(profileID string) ParserPolicyInput {
	active := true
	return ParserPolicyInput{
		DocumentType:     "pdf_table",
		ParseMode:        model.ParseModeBuiltin,
		ChunkMethod:      "table",
		ParserConfig:     json.RawMessage(`{"ocr":false}`),
		QualityProfileID: profileID,
		Active:           &active,
	}
}

func assertHTTPError(t *testing.T, err error, status, code int) {
	t.Helper()
	businessErr, ok := err.(*httperr.Error)
	if !ok || businessErr.Status != status || businessErr.Code != code {
		t.Fatalf("expected %d/%d, got %v", status, code, err)
	}
}

func TestQualityProfilePersistsCanonicalPayload(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	created, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Metrics != `{"text_health":{"weight":1,"enabled":true}}` || created.Thresholds != `{}` {
		t.Fatalf("unexpected quality profile: %+v", created)
	}
	found, err := svc.GetQualityProfile(ctx, "tenant-1", created.ID)
	if err != nil || found == nil || found.Name != "pdf-quality-v1" {
		t.Fatalf("quality profile not persisted: %v %+v", err, found)
	}
}

func TestQualityProfileRejectsInvalidWeightsAndNames(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	input := qualityProfileInput("bad-weights")
	input.Metrics = map[string]QualityMetric{"text_health": qualityMetric(.7)}
	_, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", input)
	assertHTTPError(t, err, 400, 40082)

	if _, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1")); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	assertHTTPError(t, err, 409, 40981)

	second, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v2"))
	if err != nil {
		t.Fatal(err)
	}
	name := "pdf-quality-v1"
	_, err = svc.UpdateQualityProfile(ctx, "tenant-1", "user-1", second.ID, QualityProfilePatchInput{Name: &name})
	assertHTTPError(t, err, 409, 40981)
}

func TestQualityProfileRequiresReferenceThresholdsAndGoldenSet(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	input := qualityProfileInput("reference-quality-v1")
	input.EvaluationMode = model.QualityEvaluationHeuristicPlusReference
	input.ReferenceWeight = .2
	input.ReferenceHardFail = true
	_, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", input)
	assertHTTPError(t, err, 400, 40082)

	input.Thresholds = map[string]float64{
		"table_recall": 0.9, "reading_order_accuracy": 0.9,
		"header_footer_precision": 0.9, "ocr_character_accuracy": 0.9,
	}
	_, err = svc.CreateQualityProfile(ctx, "tenant-1", "user-1", input)
	assertHTTPError(t, err, 400, 40082)
}

func TestParserPolicyLifecycleAndActiveConflict(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	if created.ParserConfig != `{"ocr":false}` || created.FallbackPolicy != `{}` {
		t.Fatalf("canonical JSON mismatch: %+v", created)
	}
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	assertHTTPError(t, err, 409, 40981)

	inactive := false
	_, err = svc.UpdateParserPolicy(ctx, "tenant-1", created.ID, ParserPolicyPatchInput{Active: &inactive})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateParserPolicy(ctx, "tenant-1", created.ID, ParserPolicyPatchInput{Active: &[]bool{true}[0]})
	assertHTTPError(t, err, 409, 40981)
	if err := svc.DeleteParserPolicy(ctx, "tenant-1", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteParserPolicy(ctx, "tenant-1", second.ID); err != nil {
		t.Fatal(err)
	}
}

func TestParserPolicyValidatesModeAndQualityProfile(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	input := parserPolicyInput(profile.ID)
	input.ParseMode = model.ParseModePipeline
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", input)
	assertHTTPError(t, err, 400, 40081)

	input = parserPolicyInput("missing-profile")
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", input)
	assertHTTPError(t, err, 400, 40081)

	invalidConfig := parserPolicyInput(profile.ID)
	invalidConfig.ParserConfig = json.RawMessage(`[]`)
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", invalidConfig)
	assertHTTPError(t, err, 400, 40081)
}

func TestParserPolicyValidatesPipelineWithRAGFlowProvider(t *testing.T) {
	ctx := context.Background()
	svc := newParserPolicyTestService(t)
	mock := ragflow.NewMock()
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "general"}, map[string]interface{}{"graph": true})
	svc.RAGFlow = mock
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	input := parserPolicyInput(profile.ID)
	input.ParseMode = model.ParseModePipeline
	input.ChunkMethod = ""
	input.PipelineID = "general"
	created, err := svc.CreateParserPolicy(ctx, "tenant-1", input)
	if err != nil {
		t.Fatalf("create pipeline policy: %v", err)
	}
	if created.PipelineID != "general" || created.ChunkMethod != "" {
		t.Fatalf("unexpected pipeline policy: %+v", created)
	}

	input.PipelineID = "missing"
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", input)
	assertHTTPError(t, err, 400, 40081)
	if mock.LastPipelineID() != "missing" {
		t.Fatalf("pipeline provider was not called: %q", mock.LastPipelineID())
	}
}

func TestParserPolicyFailsClosedWithoutRAGFlowProvider(t *testing.T) {
	ctx := context.Background()
	svc := newParserPolicyTestService(t)
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	input := parserPolicyInput(profile.ID)
	input.ParseMode = model.ParseModePipeline
	input.ChunkMethod = ""
	input.PipelineID = "general"
	_, err = svc.CreateParserPolicy(ctx, "tenant-1", input)
	assertHTTPError(t, err, 503, 50302)
}

func TestParserPolicyRejectsFallbackCycleAndProtectedDeletion(t *testing.T) {
	svc := newParserPolicyTestService(t)
	ctx := context.Background()
	profile, err := svc.CreateQualityProfile(ctx, "tenant-1", "user-1", qualityProfileInput("pdf-quality-v1"))
	if err != nil {
		t.Fatal(err)
	}
	primary, err := svc.CreateParserPolicy(ctx, "tenant-1", parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	fallbackInput := parserPolicyInput(profile.ID)
	fallbackInput.DocumentType = "pdf_text"
	fallbackInput.ChunkMethod = "general"
	fallbackPolicy, err := svc.CreateParserPolicy(ctx, "tenant-1", fallbackInput)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"fallback_policy_id":"` + fallbackPolicy.ID + `","max_attempts":2}`)
	if _, err := svc.UpdateParserPolicy(ctx, "tenant-1", primary.ID, ParserPolicyPatchInput{FallbackPolicy: &raw}); err != nil {
		t.Fatal(err)
	}
	backRaw := json.RawMessage(`{"fallback_policy_id":"` + primary.ID + `","max_attempts":2}`)
	_, err = svc.UpdateParserPolicy(ctx, "tenant-1", fallbackPolicy.ID, ParserPolicyPatchInput{FallbackPolicy: &backRaw})
	assertHTTPError(t, err, 400, 40081)

	selfRaw := json.RawMessage(`{"fallback_policy_id":"` + fallbackPolicy.ID + `","max_attempts":2}`)
	_, err = svc.UpdateParserPolicy(ctx, "tenant-1", fallbackPolicy.ID, ParserPolicyPatchInput{FallbackPolicy: &selfRaw})
	assertHTTPError(t, err, 400, 40081)

	if err := svc.DeleteParserPolicy(ctx, "tenant-1", fallbackPolicy.ID); err == nil {
		t.Fatal("expected fallback deletion to be blocked")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 409 || businessErr.Code != 40981 {
		t.Fatalf("unexpected fallback deletion error: %v", err)
	}
	if err := svc.DeleteQualityProfile(ctx, "tenant-1", profile.ID); err == nil {
		t.Fatal("expected quality profile deletion to be blocked")
	} else if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 409 || businessErr.Code != 40981 {
		t.Fatalf("unexpected quality profile deletion error: %v", err)
	}
}
