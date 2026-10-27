package service

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// docxDocumentXML extracts word/document.xml from the rendered package so
// assertions run against the real markup instead of compressed bytes.
func docxDocumentXML(t *testing.T, doc []byte) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatalf("invalid docx package: %v", err)
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		fileReader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(fileReader)
		_ = fileReader.Close()
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Fatal("docx package missing word/document.xml")
	return ""
}

func TestParseExecutionStepsNormalizesAndWhitelists(t *testing.T) {
	raw := `[
		{"label":"retrieval","status":"success","duration_ms":1200,"summary":"已检索 2 条","tool_args":{"secret":"must-not-render"}},
		{"name":"classify","status":"running","duration_ms":30},
		{"label":"execute","status":"weird-status","summary":"partial"},
		{"status":"failed"}
	]`
	steps := parseExecutionSteps(raw)
	if len(steps) != 3 {
		t.Fatalf("expected 3 named steps, got %d: %+v", len(steps), steps)
	}
	if steps[0].label != "retrieval" || steps[0].status != executionStepStatusSuccess || steps[0].durationMs != 1200 {
		t.Fatalf("unexpected first step: %+v", steps[0])
	}
	if strings.Contains(steps[0].summary, "secret") || strings.Contains(steps[0].summary, "tool_args") {
		t.Fatalf("tool args leaked into summary: %+v", steps[0])
	}
	if steps[1].label != "classify" || steps[1].status != executionStepStatusRunning {
		t.Fatalf("name fallback failed: %+v", steps[1])
	}
	if steps[2].status != executionStepStatusFailed {
		t.Fatalf("unknown status must normalize to failed: %+v", steps[2])
	}
}

func TestParseExecutionStepsTruncates(t *testing.T) {
	longLabel := strings.Repeat("标", 300)
	longSummary := strings.Repeat("摘要", 300)
	steps := parseExecutionSteps(`[{"label":"` + longLabel + `","status":"success","summary":"` + longSummary + `"}]`)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if got := len([]rune(steps[0].label)); got != 128 {
		t.Fatalf("label must truncate to 128 runes, got %d", got)
	}
	if got := len([]rune(steps[0].summary)); got != 256 {
		t.Fatalf("summary must truncate to 256 runes, got %d", got)
	}
}

func TestParseExecutionStepsDegradesOnDirtyData(t *testing.T) {
	for _, raw := range []string{
		"",
		"[]",
		"null",
		"not-json",
		`{"label":"object-not-array"}`,
		`[{"summary":"no label or name"}]`,
	} {
		if steps := parseExecutionSteps(raw); steps != nil {
			t.Fatalf("dirty input %q must degrade to nil, got %+v", raw, steps)
		}
	}
}

func TestParseExecutionStepsDropsNegativeDurationButKeepsStep(t *testing.T) {
	steps := parseExecutionSteps(`[{"label":"step","status":"success","duration_ms":-5}]`)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].durationMs != 0 {
		t.Fatalf("negative duration must be dropped, got %d", steps[0].durationMs)
	}
}

func TestRenderDocxIncludesAgentTimeline(t *testing.T) {
	snapshot := &model.AnswerSnapshot{
		ID: "snap-timeline", AnswerSchemaVersion: model.AnswerSchemaVersion,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		Content:       "答案正文",
		CitationsJSON: "[]",
		ExecutionJSON: `[{"label":"retrieval","status":"success","duration_ms":1200,"summary":"已检索 2 条","tool_args":{"secret":"x"}}]`,
	}
	projection := &model.AuthorizationProjection{
		AnswerVisibility:   model.AuthorizationVisibilityVisible,
		ContentVisibility:  model.AuthorizationVisibilityVisible,
		ArtifactVisibility: model.AuthorizationVisibilityVisible,
	}
	doc, err := renderDocx(snapshot, projection, &model.ExportJob{TemplateVersion: 1, PolicyVersion: "auth.v1"}, "export-1")
	if err != nil {
		t.Fatal(err)
	}
	body := docxDocumentXML(t, doc)
	for _, want := range []string{"Agent Execution Timeline", "retrieval", "1200 ms", "已检索 2 条"} {
		if !strings.Contains(body, want) {
			t.Fatalf("docx missing %q", want)
		}
	}
	if strings.Contains(body, "must-not-render") || strings.Contains(body, "secret") {
		t.Fatal("docx leaked tool args")
	}
}

func TestRenderDocxSkipsTimelineForEmptyExecution(t *testing.T) {
	snapshot := &model.AnswerSnapshot{
		ID: "snap-empty", AnswerSchemaVersion: model.AnswerSchemaVersion,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		Content: "正文", CitationsJSON: "[]", ExecutionJSON: "[]",
	}
	projection := &model.AuthorizationProjection{
		AnswerVisibility:   model.AuthorizationVisibilityVisible,
		ContentVisibility:  model.AuthorizationVisibilityVisible,
		ArtifactVisibility: model.AuthorizationVisibilityVisible,
	}
	doc, err := renderDocx(snapshot, projection, &model.ExportJob{TemplateVersion: 1, PolicyVersion: "auth.v1"}, "export-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(docxDocumentXML(t, doc), "Agent Execution Timeline") {
		t.Fatal("empty execution must not render a timeline section")
	}
}
