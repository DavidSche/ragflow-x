/**
 * doc/301 T08/T09/T12 — the review workbench's honesty rules.
 *
 * These tests exist because each of them pins a way the panel could tell a
 * comfortable lie: print 0% for "no denominator", call a queued run "not eligible",
 * highlight a quote that matched two places, or offer a decision the server would
 * reject.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { tZh, translateFor } from "../../../test/translate";
import { AssuranceReviewPanel } from "./AssuranceReviewPanel";
import type { ReportReview, ReviewFinding } from "./report-assurance";

// Resolve through the shipped dictionary: an assertion stays human-readable Chinese
// and still fails when a key is missing or its wording changes. The locale is
// switchable so one test can prove the panel has no Chinese of its own.
const locale = vi.hoisted(() => ({ current: "zh" as "zh" | "en" }));
vi.mock("ra-core", () => ({
  useTranslate: () => (locale.current === "en" ? translateFor("en") : tZh),
}));
vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/button", () => ({
  Button: (props: Record<string, unknown>) => <button {...props} />,
}));

function reviewOf(overrides: Partial<ReportReview> = {}): ReportReview {
  return {
    report_id: "rep1",
    revision_id: "rev2",
    revision: 2,
    revision_status: "published",
    release_eligibility: "tier_1",
    release_eligibility_basis: ["coverage", "supported_rate"],
    requested_policy: "advisory",
    acknowledgeable: false,
    acknowledged: false,
    counters: {
      total_claims: 10,
      denominator: 8,
      evaluated: 8,
      supported: 6,
      partially_supported: 1,
      unsupported: 1,
      conflicting: 0,
      insufficient_evidence: 0,
      abstained: 0,
      not_evaluated: 2,
      not_required: 2,
      high_findings: 1,
      open_findings: 1,
      not_required_share: 0.2,
      denominator_small: false,
      coverage: 0.8,
    },
    sections: [
      {
        ord: 1,
        section_key: "summary",
        title: "执行摘要",
        requires_evidence: true,
        claims: [
          {
            id: "c1",
            ord: 1,
            claim_text: "2023 年营收同比增长 12%",
            support_verdict: "supported",
            quote_status: "verified",
            quote_text: "2023 年营收同比增长 12%",
            quote_span_end: 18,
            matched_snapshot_id: "snap1",
            cache_hit: false,
          },
        ],
      },
    ],
    findings: [],
    // The contrast scope is required (doc/301 §5.3.1): a projection that did not state
    // what it compared against would let the panel imply a contrast that never ran, so
    // the fixture states the weakest true value (`cited_only`) rather than nothing.
    scan_scope: "cited_only",
    candidate_count: 0,
    candidates_visible: false,
    ...overrides,
  };
}

describe("AssuranceReviewPanel", () => {
  it("shows the tier AND the basis, so the number never stands alone", async () => {
    render(<AssuranceReviewPanel load={async () => reviewOf()} />);
    expect(await screen.findByTestId("eligibility-tier")).toHaveTextContent("tier_1");
    expect(screen.getByTestId("eligibility-basis")).toHaveTextContent("coverage");
    expect(screen.getByTestId("eligibility-basis")).toHaveTextContent("supported_rate");
  });

  it("renders a null rate as a word, never as 0% and never as a bare dash", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            release_eligibility: "tier_0",
            counters: { ...reviewOf().counters, coverage: null, not_required_share: null },
          })}
      />,
    );
    // A dash is silent: "this build has no field" and "nobody filled the cell" look
    // identical, and both read as 0% next to the neighbouring percentage.
    expect(await screen.findByTestId("counter-coverage")).toHaveTextContent("未测量");
    expect(screen.getByTestId("counter-coverage")).not.toHaveTextContent("0.0%");
    expect(screen.getByTestId("counter-coverage")).not.toHaveTextContent("—");
    expect(screen.getByTestId("counter-not-required")).toHaveTextContent("未测量");
  });

  it("word a pending run as 'not measured yet', not as a failure", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            release_eligibility: "pending",
            release_eligibility_basis: ["not_run"],
            run: undefined,
          })}
      />,
    );
    const reason = await screen.findByTestId("eligibility-reason");
    expect(reason).toHaveTextContent("尚未完成复核");
    expect(reason).not.toHaveTextContent("不满足门槛");
  });

  it("never highlights an ambiguous quote and says why", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            sections: [
              {
                ord: 1,
                section_key: "summary",
                title: "执行摘要",
                requires_evidence: true,
                claims: [
                  {
                    id: "c2",
                    ord: 1,
                    claim_text: "毛利率为 31%",
                    support_verdict: "partially_supported",
                    quote_status: "ambiguous",
                    quote_text: "毛利率为 31%",
                    quote_span_end: 8,
                    matched_snapshot_id: "snap1",
                    cache_hit: false,
                  },
                ],
              },
            ],
          })}
      />,
    );
    expect(await screen.findByTestId("claim-quote-c2")).toHaveTextContent("无法唯一定位");
    // The claim text and the quote text are the same string here, which is the whole
    // point: whichever one the panel prints as the quote must NOT be highlighted.
    const nodes = screen.getAllByText("毛利率为 31%");
    expect(nodes.some((node) => node.className.includes("line-through"))).toBe(true);
  });

  it("surfaces the small-denominator caveat next to the coverage figure", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            counters: {
              ...reviewOf().counters,
              not_required_share: 0.55,
              denominator_small: true,
            },
          })}
      />,
    );
    expect(await screen.findByTestId("coverage-caveat")).toHaveTextContent("55.0%");
    expect(screen.getByTestId("coverage-caveat")).toHaveTextContent("不宜据此判定质量门槛");
  });

  it("offers no adjudication when the caller cannot decide (grantee)", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            findings: [
              {
                id: "f1",
                section_ord: 1,
                source: "citation_completeness",
                category: "missing_evidence",
                severity: "high",
                status: "open",
                title: "该段需要有证据支持的结论缺少引文",
                created_at: "2026-02-14T00:00:00Z",
              },
            ],
          })}
      />,
    );
    expect(await screen.findByTestId("finding-f1")).toHaveTextContent("缺少引文");
    expect(screen.queryByText("已阅")).toBeNull();
    expect(screen.queryByText("驳回（误报）")).toBeNull();
  });

  it("drives the closed transition table: 已阅 then 标记已修复, and resolves with a later revision", async () => {
    const decisions: Array<{ id: string; status: string; resolvedRevisionId?: string }> = [];
    const decide = async (
      id: string,
      body: { status: string; note?: string; resolvedRevisionId?: string },
    ): Promise<ReviewFinding> => {
      decisions.push({ id, status: body.status, resolvedRevisionId: body.resolvedRevisionId });
      return {
        id,
        section_ord: 1,
        source: "citation_completeness",
        category: "missing_evidence",
        severity: "high",
        status: body.status as ReviewFinding["status"],
        created_at: "2026-02-14T00:00:00Z",
        resolved_revision_id: body.resolvedRevisionId,
      };
    };
    const findings: ReviewFinding[] = [
      {
        id: "f9",
        section_ord: 1,
        source: "citation_completeness",
        category: "missing_evidence",
        severity: "high",
        status: "acknowledged",
        title: "缺引文",
        created_at: "2026-02-14T00:00:00Z",
      },
    ];
    render(
      <AssuranceReviewPanel
        load={async () => reviewOf({ findings })}
        decide={decide}
        laterRevisions={[{ id: "rev3", revision: 3 }, { id: "rev4", revision: 4 }]}
        revisionIdToNumber={{ rev3: 3 }}
      />,
    );
    await screen.findByTestId("finding-f9");
    // `resolved` needs a target revision, so the button starts disabled.
    const resolve = screen.getByText("标记已修复");
    expect(resolve).toBeDisabled();
    await userEvent.selectOptions(screen.getByRole("combobox"), "rev3");
    await waitFor(() => expect(screen.getByText("标记已修复")).not.toBeDisabled());
    await userEvent.click(screen.getByText("标记已修复"));
    await waitFor(() => expect(decisions).toEqual([{ id: "f9", status: "resolved", resolvedRevisionId: "rev3" }]));
  });

  it("tells the reader an unresolved conflict blocks export until acknowledged", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({ acknowledgeable: true, acknowledged: false, requested_policy: "required" })}
      />,
    );
    expect(await screen.findByTestId("ack-required")).toHaveTextContent("导出前需确认知悉");
    expect(screen.queryByTestId("ack-flag")).toBeNull();
  });

  it("has no 发起复核 button when triggering is not permitted", async () => {
    render(<AssuranceReviewPanel load={async () => reviewOf()} />);
    await screen.findByTestId("eligibility-tier");
    expect(screen.queryByText("发起复核")).toBeNull();
    expect(screen.queryByText("重新复核")).toBeNull();
  });

  it("reports a truncated projection instead of pretending the list is complete", async () => {
    render(
      <AssuranceReviewPanel
        load={async () => reviewOf({ truncated: ["sections", "claims"] })}
      />,
    );
    expect(await screen.findByTestId("truncated-note")).toHaveTextContent("claims");
  });

  // The panel used to hold its prose as Chinese constants (a verdict label map, a
  // copy of the quote-status wording, button text). An English build rendered it all
  // in Chinese, and no zh/en key-parity test could see it — the words were not keys.
  // This renders the whole panel in `en` and refuses any CJK character on screen.
  it("renders no Chinese of its own in the en locale", async () => {
    locale.current = "en";
    try {
      const { container } = render(
        <AssuranceReviewPanel
          load={async () =>
            reviewOf({
              acknowledgeable: true,
              truncated: ["sections"],
              // Server content is echoed verbatim (a report is the tenant's words),
              // so the fixture is ASCII here: anything CJK left on screen can only
              // come from the panel's own prose.
              sections: [
                {
                  ord: 1,
                  section_key: "summary",
                  title: "Executive summary",
                  requires_evidence: true,
                  claims: [
                    {
                      id: "c1",
                      ord: 1,
                      claim_text: "Revenue grew 12%",
                      support_verdict: "supported",
                      quote_status: "verified",
                      quote_text: "Revenue grew 12%",
                      quote_span_end: 15,
                      matched_snapshot_id: "snap1",
                      cache_hit: false,
                    },
                  ],
                },
              ],
              findings: [
                {
                  id: "f1",
                  run_id: "run1",
                  revision_id: "rev2",
                  section_ord: 1,
                  severity: "high",
                  category: "missing_evidence",
                  source: "deterministic",
                  status: "open",
                  title: "",
                  detail: "d1",
                  suggested_action: "add a citation",
                  created_at: "2026-11-20T00:00:00Z",
                } as ReviewFinding,
              ],
            })}
          decide={async () => ({}) as ReviewFinding}
          laterRevisions={[{ id: "rev3", revision: 3 }]}
        />,
      );
      await screen.findByTestId("finding-f1");
      const cjk = /[㐀-䶿一-鿿豈-﫿]/;
      expect(container.textContent ?? "").not.toMatch(cjk);
      // Server data is passed through verbatim, so the finding title stays as sent
      // while its category is translated — evidence the words come from the dictionary.
      expect(screen.getByTestId("finding-f1")).toHaveTextContent("Missing citation");
    } finally {
      locale.current = "zh";
    }
  });
});

/**
 * 反证候选与复核范围 (doc/301 §9 / §5.3.1).
 *
 * The panel has to disclose *what was compared against* and *whose text it is
 * showing*. Two of these tests are about the sentences a reader must not be able to
 * misread; two are about the owner-only masking, which is a data-visibility rule and
 * therefore must be asserted on the rendered text rather than on a flag.
 */
describe("AssuranceReviewPanel contrast scope", () => {
  const ownerCandidates = [
    { section_ord: 1, pool_rank: 3, document_label: "差旅制度-v2.pdf", content: "住宿标准为每晚上限 500 元。" },
    { section_ord: 1, pool_rank: 4, content: "会议差旅可申请特批。", truncated: true },
  ];

  it("says no contrast happened when the revision froze no candidate pool", async () => {
    render(<AssuranceReviewPanel load={async () => reviewOf({ scan_scope: "cited_only" })} />);
    expect(await screen.findByTestId("review-scan-scope")).toHaveTextContent("本次未做跨文档冲突对照");
    expect(screen.queryByTestId("review-candidates")).not.toBeInTheDocument();
    expect(screen.queryByTestId("review-candidates-hidden")).not.toBeInTheDocument();
  });

  it("shows the owner the frozen excerpts, with section and pool rank but no identifier", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            scan_scope: "cited+candidate:8",
            candidate_count: 8,
            candidates_visible: true,
            candidates: ownerCandidates,
          })}
      />,
    );
    const scope = await screen.findByTestId("review-scan-scope");
    expect(scope).toHaveTextContent("未引用候选 8 条");
    expect(scope).toHaveTextContent("候选不入证据链");

    const first = screen.getByTestId("review-candidate-1-3");
    expect(first).toHaveTextContent("第 1 节");
    expect(first).toHaveTextContent("候选 #3");
    expect(first).toHaveTextContent("差旅制度-v2.pdf");
    expect(first).toHaveTextContent("住宿标准为每晚上限 500 元。");

    // The second candidate has no document label (a document cited nowhere in this
    // revision legitimately has none) and is marked as an excerpt.
    const second = screen.getByTestId("review-candidate-1-4");
    expect(second).toHaveTextContent("会议差旅可申请特批。");
    expect(second).toHaveTextContent("节选");

    // A candidate is not evidence: no chunk id, no document id, no retrieval score.
    const rendered = screen.getByTestId("review-contrast-scope").textContent ?? "";
    expect(rendered).not.toContain("chunk-");
    expect(rendered).not.toContain("document_id");
    expect(rendered).not.toMatch(/\b0\.\d{2}\b/);
  });

  it("tells a grantee that N candidates took part, without showing their text", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            scan_scope: "cited+candidate:8",
            candidate_count: 8,
            candidates_visible: false,
            candidates: [],
          })}
      />,
    );
    const hidden = await screen.findByTestId("review-candidates-hidden");
    expect(hidden).toHaveTextContent("另有 8 条未引用候选参与对照");
    expect(hidden).toHaveTextContent("正文仅报告所有者可见");
    expect(screen.queryByTestId("review-candidates")).not.toBeInTheDocument();
    expect(screen.getByTestId("review-contrast-scope").textContent ?? "").not.toContain("住宿标准");
  });

  it("marks a truncated candidate list instead of implying the whole pool was shown", async () => {
    render(
      <AssuranceReviewPanel
        load={async () =>
          reviewOf({
            scan_scope: "cited+candidate:8",
            candidate_count: 8,
            candidates_visible: true,
            candidates: ownerCandidates,
            candidates_truncated: true,
          })}
      />,
    );
    const note = await screen.findByTestId("review-candidates-truncated");
    expect(note).toHaveTextContent("仅列出 2 条");
    expect(note).toHaveTextContent("对照已覆盖全部候选");
  });

  it("shows the writing-time pool beside the section, and omits what was never recorded", async () => {
    // doc/301 §5.8 item 1. Two sections, deliberately different shapes: the first
    // recorded its pool, the second predates the column. The absent case must render
    // *nothing* — "the writer had no evidence to read" and "nobody wrote the field"
    // are different statements and only the first one is about the corpus.
    const base = reviewOf();
    render(
      <AssuranceReviewPanel
        load={async () => ({
          ...base,
          sections: [
            {
              ...base.sections[0],
              pool_hit_chunks: 6,
              pool_insufficient: true,
              pool_candidate_count: 4,
            },
            { ord: 2, section_key: "risks", title: "风险", requires_evidence: true, claims: [] },
          ],
        })}
      />,
    );
    const recorded = await screen.findByTestId("review-section-context-1");
    expect(recorded).toHaveTextContent("写作时证据池 6 条");
    expect(recorded).toHaveTextContent("低于模板最低证据条数");
    expect(recorded).toHaveTextContent("未引用 4 条");
    expect(screen.queryByTestId("review-section-context-2")).toBeNull();
  });

  it("names a degraded research plan in words, never with the machine token", async () => {
    const base = reviewOf();
    render(
      <AssuranceReviewPanel
        load={async () => ({
          ...base,
          sections: [{ ...base.sections[0], research_planning: "fallback_budget" }],
        })}
      />,
    );
    const degraded = await screen.findByTestId("review-section-degraded-1");
    expect(degraded).toHaveTextContent("深研未启用：预算不足");
    expect(degraded.textContent ?? "").not.toContain("fallback_budget");
  });

  it("stacks the three columns below the wide breakpoint instead of clipping them", async () => {
    // T08 DoD: the workbench is designed for ≥1280px, and at 1024px or on a tablet the
    // three fixed panes would each be ~200px of cut-off text. jsdom has no layout
    // engine, so the only mechanical pin available is the layout contract itself: a
    // single-column default plus a `lg:` three-column rule. A regression to
    // `grid-cols-[…]` without the default would fail here.
    const { container } = render(<AssuranceReviewPanel load={async () => reviewOf()} />);
    await screen.findByTestId("eligibility-tier");
    const grid = container.querySelector('[data-testid="review-body-grid"]');
    expect(grid).not.toBeNull();
    const classes = grid?.className ?? "";
    expect(classes).toContain("grid-cols-1");
    expect(classes).toContain("lg:grid-cols-[");
    expect(classes).not.toMatch(/(^|\s)grid-cols-\[/);
  });

  it("has no Chinese of its own in the contrast block", async () => {
    // Server data (document label, excerpt) is the tenant's own text and may be in any
    // language; the assertion is about the panel's *copy*, so this leg uses ASCII data
    // and would still fail on a hardcoded Chinese label.
    locale.current = "en";
    try {
      render(
        <AssuranceReviewPanel
          load={async () =>
            reviewOf({
              scan_scope: "cited+candidate:8",
              candidate_count: 8,
              candidates_visible: true,
              candidates: [
                { section_ord: 1, pool_rank: 3, document_label: "travel-policy-v2.pdf", content: "Lodging cap is 500 CNY per night." },
              ],
              candidates_truncated: true,
            })}
        />,
      );
      const block = await screen.findByTestId("review-contrast-scope");
      expect(block.textContent ?? "").not.toMatch(/[\u4e00-\u9fff]/);
      expect(block).toHaveTextContent("not evidence");
    } finally {
      locale.current = "zh";
    }
  });
});
