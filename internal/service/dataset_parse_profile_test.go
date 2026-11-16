package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// parseProfileStub is a Mock engine with a fixed document set and call
// recording. The repair write channel is the thing that broke in the field
// (doc/285): the wizard proposed `apply_parse_profile`, the operator clicked it,
// and the endpoint answered 40915 because nobody sent rewrite_existing.
type parseProfileStub struct {
	ragflow.Mock
	docs       []ragflow.Document
	parseCalls int
	parseIDs   []string
	updatedTo  string
}

func (p *parseProfileStub) ListDocuments(ctx context.Context, datasetID string) ([]ragflow.Document, error) {
	return p.docs, nil
}

func (p *parseProfileStub) ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	p.parseCalls++
	p.parseIDs = append(p.parseIDs, documentIDs...)
	return nil
}

func (p *parseProfileStub) UpdateDatasetConfig(ctx context.Context, datasetID string, cfg ragflow.DatasetConfigUpdate) error {
	if cfg.ChunkMethod != nil {
		p.updatedTo = *cfg.ChunkMethod
	}
	return nil
}

func newParseProfileService(t *testing.T, docs []ragflow.Document) (*Service, string, string, *parseProfileStub) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: testSQLiteDSN(t, "parse-profile.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	stub := &parseProfileStub{Mock: *ragflow.NewMock(), docs: docs}
	svc := New(repository.NewStore(gdb), stub, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "parse-profile-tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "raven-guide")
	if err != nil {
		t.Fatal(err)
	}
	return svc, tenant.ID, dataset.ID, stub
}

// TestApplyParseProfileRefusesParsedDocuments pins the guard the operator hit:
// the refusal must name the opt-in instead of silently discarding chunks.
func TestApplyParseProfileRefusesParsedDocuments(t *testing.T) {
	svc, tenantID, datasetID, stub := newParseProfileService(t, []ragflow.Document{
		{ID: "doc-1", Name: "guide.pdf", Enabled: "1", ChunkCount: 12},
	})
	_, err := svc.ApplyParseProfile(context.Background(), tenantID, "", datasetID, ParseProfileInput{
		ChunkMethod: "book",
	})
	if err == nil {
		t.Fatal("parsed documents without rewrite_existing must be refused")
	}
	if code := errCode(err); code != codeParseProfileRequiresRewrite {
		t.Fatalf("expected %d, got %d (%v)", codeParseProfileRequiresRewrite, code, err)
	}
	if stub.updatedTo != "" || stub.parseCalls != 0 {
		t.Fatalf("a refused repair must not write anything, updated=%q parseCalls=%d", stub.updatedTo, stub.parseCalls)
	}
}

// TestApplyParseProfileWithRewriteReparses is the repair the wizard actually
// needs: it writes the template's chunk method and re-parses under it.
func TestApplyParseProfileWithRewriteReparses(t *testing.T) {
	svc, tenantID, datasetID, stub := newParseProfileService(t, []ragflow.Document{
		{ID: "doc-1", Name: "guide.pdf", Enabled: "1", ChunkCount: 12},
		{ID: "doc-2", Name: "appendix.pdf", Enabled: "1", ChunkCount: 4},
	})
	view, err := svc.ApplyParseProfile(context.Background(), tenantID, "", datasetID, ParseProfileInput{
		ChunkMethod:     "book",
		RewriteExisting: true,
	})
	if err != nil {
		t.Fatalf("rewrite_existing=true must succeed, got %v", err)
	}
	if stub.updatedTo != "book" {
		t.Fatalf("chunk method must be written, got %q", stub.updatedTo)
	}
	if !view.ReparseTriggered || stub.parseCalls != 1 {
		t.Fatalf("documents must be re-parsed once, triggered=%v calls=%d", view.ReparseTriggered, stub.parseCalls)
	}
	if len(stub.parseIDs) != 2 {
		t.Fatalf("both documents must be re-parsed, got %v", stub.parseIDs)
	}
}

// TestApplyParseProfileEmptyDatasetNeedsNoRewrite keeps the guard from becoming
// a blanket acknowledgement: with nothing parsed the repair is non-destructive.
func TestApplyParseProfileEmptyDatasetNeedsNoRewrite(t *testing.T) {
	svc, tenantID, datasetID, stub := newParseProfileService(t, nil)
	view, err := svc.ApplyParseProfile(context.Background(), tenantID, "", datasetID, ParseProfileInput{
		ChunkMethod: "book",
	})
	if err != nil {
		t.Fatalf("an empty dataset must not require rewrite_existing, got %v", err)
	}
	if view.ReparseTriggered || stub.parseCalls != 0 {
		t.Fatalf("nothing to re-parse, triggered=%v calls=%d", view.ReparseTriggered, stub.parseCalls)
	}
	if stub.updatedTo != "book" {
		t.Fatalf("chunk method must still be written, got %q", stub.updatedTo)
	}
}

// TestRepairTargetIsApplicable guards the link between the two halves of the
// repair: whatever chunk method the check proposes must be one the write
// channel accepts. A `naive` default (the old wizard fallback) is exactly the
// kind of drift this pins.
func TestRepairTargetIsApplicable(t *testing.T) {
	contract := &ScenarioTemplateKnowledge{
		Datasets: []ScenarioKnowledgeDataset{{
			Role:        scenarioDatasetRolePrimary,
			ParseIntent: &ScenarioParseIntent{Mode: model.ParseModeBuiltin, ChunkMethod: "book"},
		}},
	}
	verdict := EvaluateRequirements(contract, scenarioDatasetRolePrimary, DatasetCapabilityProfile{
		DatasetID: "ds-x", ChunkMethod: "general", DocumentCount: 1, ParsedCount: 1,
	})
	for _, repair := range verdict.Repairs {
		if repair.Action != "apply_parse_profile" {
			continue
		}
		if strings.TrimSpace(repair.ChunkMethod) == "" {
			t.Fatalf("repair must carry the target chunk method: %+v", repair)
		}
		if !isKnownChunkMethod(normalizeChunkMethod(repair.ChunkMethod)) {
			t.Fatalf("proposed chunk method %q is not writable", repair.ChunkMethod)
		}
		return
	}
	t.Fatalf("expected an apply_parse_profile repair, got %+v", verdict.Repairs)
}
