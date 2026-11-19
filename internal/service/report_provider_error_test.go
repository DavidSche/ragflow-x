package service

// A64: the engine's component-error envelope never becomes report content.
//
// Live, 2026-11-19: the engine's model route hit its hard deadline and answered
// `**ERROR**: API request failed with status 502: {"error":"proxy upstream error:
// hard deadline (absolute)"}` with HTTP 200 and a normal chat envelope. The report
// shipped that string as five section bodies and ten claim rows, and graded every
// one of them `unsupported` — a wrong deliverable that looked like an honest
// "sources do not cover this". The envelope is a *failed call*, so the section must
// retry and, if it keeps failing, close `failed` and be disclosed as a gap.

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// reportProviderErrorEnvelopeReply is the engine's failure shape, verbatim from the
// live environment (doc/environment/verification/45_*.md §4 records the same
// convention for a canvas component).
const reportProviderErrorEnvelopeReply = `**ERROR**: API request failed with status 502: ` +
	`{"error":"proxy upstream error: hard deadline (absolute)"}`

func TestReportModelErrorContent(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantDetail string
	}{
		{
			name: "engine envelope is not content",
			text: reportProviderErrorEnvelopeReply,
			wantDetail: `API request failed with status 502: ` +
				`{"error":"proxy upstream error: hard deadline (absolute)"}`,
		},
		{
			name: "leading whitespace does not hide it",
			text: "\n  **ERROR**: component: invalid param\n", wantDetail: "component: invalid param",
		},
		{
			// A section may legitimately quote an error string inside a table; only a
			// reply that *is* the envelope is refused.
			name: "a body that quotes an error is still a body",
			text: "## 3. 处置\n\n- 接口返回 **ERROR**: 502\n- 已重试并记录",
		},
		{
			// Multi-line replies are answers: the engine's envelope is one message.
			name: "a multi-line reply is not the envelope",
			text: "**ERROR**: something\n\n补注：这一节仍然给出了正文。",
		},
		{
			name: "prose is prose",
			text: "依据《差旅管理办法》第 12 条，住宿标准为每晚上限 600 元 [1]。",
		},
		{
			name: "the prefix alone is not an envelope",
			text: "**ERROR**:   \n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			detail, isError := reportModelErrorContent(testCase.text)
			if testCase.wantDetail == "" {
				if isError {
					t.Fatalf("must not read as an error envelope, got detail %q", detail)
				}
				return
			}
			if !isError {
				t.Fatalf("must read as an error envelope:\n%s", testCase.text)
			}
			if detail != testCase.wantDetail {
				t.Fatalf("detail must be the provider's own words: %q", detail)
			}
		})
	}
}

// TestReportSectionRefusesProviderErrorEnvelope drives the writer with an engine
// that only answers with the envelope: the sections must end `failed` (so the run
// is published `partial` with gaps), the body must stay empty, and no claim may
// carry the string.
func TestReportSectionRefusesProviderErrorEnvelope(t *testing.T) {
	fx, engine := newReportRunFixture(t, "provider-error-envelope")
	ctx := context.Background()

	created := fx.outlineJob(t, "link-policy-provider-error-envelope", "req-provider-error-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	engine.mu.Lock()
	// The queue's single reply repeats, so every attempt of every section gets the
	// envelope — the "provider is down" shape, not a one-off stall.
	engine.replies = []string{reportProviderErrorEnvelopeReply}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("the outline must have produced sections")
	}
	failed := 0
	for _, section := range sections {
		// A section with a failed dependency is `skipped` (§7.9 cascade), which is
		// also how it is disclosed — what must never happen is `succeeded`.
		if section.Status != model.ReportSectionFailed && section.Status != model.ReportSectionSkipped {
			t.Fatalf("section %s must be failed or skipped, got %q", section.SectionKey, section.Status)
		}
		if strings.Contains(section.ContentMD, "**ERROR**") {
			t.Fatalf("section %s must not store the envelope as its body: %q",
				section.SectionKey, section.ContentMD)
		}
		if strings.TrimSpace(section.ContentMD) != "" {
			t.Fatalf("a failed section must have no body, got %q", section.ContentMD)
		}
		if strings.Contains(section.ContentMD, "502") {
			t.Fatalf("section %s leaked the provider's detail into its body", section.SectionKey)
		}
		if section.Status != model.ReportSectionFailed {
			continue
		}
		failed++
		if section.Attempt != reportSectionMaxAttempts {
			t.Fatalf("an envelope is retryable (§7.9), so attempt must reach %d, got %d",
				reportSectionMaxAttempts, section.Attempt)
		}
		if section.GapReason != model.ReportGapFailed {
			t.Fatalf("section %s must be disclosed as a failed gap, got %q",
				section.SectionKey, section.GapReason)
		}
	}
	if failed == 0 {
		t.Fatal("at least the sections without dependencies must be closed failed")
	}

	// `gap_reason` is the closed set (`failed`), so the *cause* has to survive in the
	// event payload — otherwise a reviewer sees "failed" and has nothing to act on.
	disclosed := false
	for _, event := range fx.reportEventsOf(t, created.Job.ID, reportEventSectionFailed) {
		if strings.Contains(event.PayloadJSON, "error envelope") {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatal("the failure event must name the envelope instead of a bare 'failed'")
	}

	var claims []model.ReportClaim
	if err := fx.gdb.Where("revision_id = ?", created.Revision.ID).Find(&claims).Error; err != nil {
		t.Fatalf("load claims: %v", err)
	}
	for _, claim := range claims {
		if strings.Contains(claim.ClaimText, "**ERROR**") {
			t.Fatalf("a claim must never quote the envelope: %q", claim.ClaimText)
		}
	}

	var citations []model.ReportCitation
	if err := fx.gdb.Where("revision_id = ?", created.Revision.ID).Find(&citations).Error; err != nil {
		t.Fatalf("load citations: %v", err)
	}
	for _, citation := range citations {
		if strings.Contains(citation.Snippet, "**ERROR**") {
			t.Fatalf("an evidence snippet must never quote the envelope: %q", citation.Snippet)
		}
	}
}
