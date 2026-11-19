import { describe, expect, it } from "vitest";
import { tZh as t } from "../../test/translate";
import {
  REPORT_ACCESS_POLICIES,
  REPORT_AUTO_CONFIRM_POLICIES,
  REPORT_BUDGET_EXTENSION_STATUSES,
  REPORT_BUDGET_POLICIES,
  REPORT_GRANT_KINDS,
  REPORT_OUTLINE_CONFIRMATIONS,
  formatRatio,
  reportListViewLabel,
  reportAccessPolicyLabel,
  reportAccessPolicyTakesTeams,
  reportAccessPolicyTakesUsers,
  reportAutoConfirmPolicyLabel,
  reportBudgetExtensionMaxFor,
  reportBudgetExtensionPending,
  reportBudgetExtensionStatusLabel,
  reportBudgetPolicyLabel,
  reportAccessGrantKindLabel,
  reportOutlineConfirmationLabel,
  formatScheduleClock,
  formatTZOffset,
  reportOpenPath,
  reportCompletenessLabel,
  reportDeliveryLabel,
  reportEvidenceStatusLabel,
  reportQualityStatusLabel,
  reportRevisionStatusLabel,
  reportResearchDepthLabel,
  reportResearchPlanningLabel,
  reportScheduleCadenceLabel,
  reportScheduleRunStatusLabel,
  reportScheduleUpdateModeLabel,
  reportScheduleWhen,
  reportSectionLocationHints,
  reportSectionResearchPlan,
  reportSectionStatusLabel,
  reportStatusLabel,
  reportTemplateEditBlocker,
  reportTemplateName,
  reportTemplateSourceLabel,
  reportTemplateStatusLabel,
  shouldShowResearchPlan,
  trendPoints,
  type TrendResult,
} from "./report-center-api";

/**
 * The four switches of doc/296 are *closed sets on both sides*: Go declares the
 * slice, `report-center-api.ts` declares the tuple, and the two must hold the same
 * values in the same order. These assertions are the second half of that contract —
 * adding a policy in Go without teaching the UI its word would otherwise ship a raw
 * `selected_groups` into a Chinese panel.
 */
describe("doc/296 closed sets", () => {
  it("mirrors the four access policies and their grantees", () => {
    expect([...REPORT_ACCESS_POLICIES]).toEqual([
      "owner_only",
      "tenant_members",
      "selected_users",
      "selected_groups",
    ]);
    expect([...REPORT_GRANT_KINDS]).toEqual(["user", "team"]);
    // Which policy owns which list is a single fact, not a per-component guess: the
    // panel builds its payload from these two answers.
    expect(REPORT_ACCESS_POLICIES.filter(reportAccessPolicyTakesUsers)).toEqual([
      "selected_users",
    ]);
    expect(REPORT_ACCESS_POLICIES.filter(reportAccessPolicyTakesTeams)).toEqual([
      "selected_groups",
    ]);
    expect(reportAccessPolicyLabel(t, "tenant_members")).toBe("工作区成员");
    expect(reportAccessGrantKindLabel(t, "team")).toBe("团队");
  });

  it("mirrors the two governance switches", () => {
    expect([...REPORT_BUDGET_POLICIES]).toEqual(["enforce", "observe"]);
    expect([...REPORT_AUTO_CONFIRM_POLICIES]).toEqual(["self", "approval"]);
    expect([...REPORT_OUTLINE_CONFIRMATIONS]).toEqual(["required", "auto"]);
    // `observe` is a measurement mode, not a synonym for "off": the word says so.
    expect(reportBudgetPolicyLabel(t, "observe")).toBe("只计量不拦截");
    expect(reportAutoConfirmPolicyLabel(t, "approval")).toBe("需审批");
    expect(reportOutlineConfirmationLabel(t, "auto")).toBe("自动确认提纲");
    // An unknown value is printed raw rather than swallowed: a new server value must
    // be legible in the UI before the client catches up.
    expect(reportBudgetPolicyLabel(t, "unheard_of")).toBe("unheard_of");
  });

  it("tells an applied extension from one still waiting", () => {
    expect([...REPORT_BUDGET_EXTENSION_STATUSES]).toEqual(["applied", "pending_approval"]);
    expect(reportBudgetExtensionStatusLabel(t, "pending_approval")).toBe("等待审批");
    // The whole view is the argument, not the status string: "pending" is a property
    // of a response, and a null one (never submitted) is not pending either.
    expect(reportBudgetExtensionPending({ status: "pending_approval" } as never)).toBe(true);
    expect(reportBudgetExtensionPending({ status: "applied" } as never)).toBe(false);
    expect(reportBudgetExtensionPending(null)).toBe(false);
    // The bound quotes the server's envelope ("at most the envelope again") so the
    // form can refuse an impossible request before the round trip.
    expect(reportBudgetExtensionMaxFor(5000)).toBe(5000);
    expect(reportBudgetExtensionMaxFor(0)).toBe(0);
  });
});

/**
 * The fixtures below are the *server* shape (`ReportMetricsTrendView` in
 * `internal/service/report_metrics.go`), not a convenient one: a bucket is keyed
 * by `date`, and an unjudged ratio is `null`. Reading `bucket.bucket` (a name the
 * backend never sends) produced an empty chart that still looked plausible — the
 * kind of bug only a fixture taken from the API can catch.
 */
const serverTrend: TrendResult = {
  view: "own",
  bucket: "day",
  from: "2026-11-04T00:00:00Z",
  to: "2026-11-18T00:00:00Z",
  days: 14,
  truncated: false,
  buckets: [
    { date: "2026-11-18", revisions: 1, published: 1 },
    { date: "2026-11-16", revisions: 3, published: 2 },
  ],
  totals: {
    revisions: 4,
    published: 3,
    quality_judged: 2,
    quality_passed: 1,
    quality_gate_pass_rate: 0.5,
    citation_coverage: null,
  },
};

describe("report center view model", () => {
  it("reads the server's `date` bucket key and sorts ascending", () => {
    expect(trendPoints(serverTrend).map((point) => point.key)).toEqual([
      "2026-11-16",
      "2026-11-18",
    ]);
  });

  it("renders an unjudged ratio as a sentence, never as 0%", () => {
    expect(formatRatio(t, null)).toBe("暂无判定");
    expect(formatRatio(t, undefined)).toBe("暂无判定");
    expect(formatRatio(t, 0)).toBe("0%");
    expect(formatRatio(t, 0.5)).toBe("50%");
    expect(formatRatio(t, 1)).toBe("100%");
  });

  it("rounds a computed ratio instead of printing float noise", () => {
    expect(formatRatio(t, 0.3333)).toBe("33%");
  });

  it("opens the report's originating conversation and keeps the session", () => {
    expect(
      reportOpenPath({
        id: "r1",
        title: "t",
        template_key: "analysis",
        status: "active",
        origin_chat_id: "chat 1",
        origin_session_id: "s&1",
        created_at: "",
        expires_at: "",
      }),
    ).toBe("/conversation-center?kind=chat&targetId=chat+1&contextId=s%261");
  });

  it("does not invent an entry point for a report with no origin chat", () => {
    expect(
      reportOpenPath({
        id: "r1",
        title: "t",
        template_key: "analysis",
        status: "active",
        created_at: "",
        expires_at: "",
      }),
    ).toBeNull();
  });

  it("labels the lifecycle flag as a lifecycle word, never as a delivery state", () => {
    // `active` means "in the daily list". Calling it 进行中 made a report published
    // last week read as still generating (live 2026-11-18). `archived` is the
    // second member of the server's closed set (doc/297 §2), so it has a word of
    // its own instead of being printed raw.
    expect(reportStatusLabel(t, "active")).toBe("在用");
    expect(reportStatusLabel(t, "archived")).toBe("已归档");
    // Anything else is shown raw: a label for a state nobody writes is fiction.
    expect(reportStatusLabel(t, "deleted")).toBe("deleted");
    expect(reportStatusLabel(t, "ARCHIVED")).toBe("ARCHIVED");
  });

  it("words the three list views apart from the row's own status", () => {
    // The views are filters, not values a row can hold: 「全部」 must never read as
    // a status a report could be in.
    expect(reportListViewLabel(t, "active")).toBe("在用");
    expect(reportListViewLabel(t, "archived")).toBe("已归档");
    expect(reportListViewLabel(t, "all")).toBe("全部");
    // An unknown view is printed raw, like every other closed set here.
    expect(reportListViewLabel(t, "trash")).toBe("trash");
    // And `all` is not a lifecycle member, so the enum label resolver leaves it raw
    // rather than inventing a state.
    expect(reportStatusLabel(t, "all")).toBe("all");
  });

  it("labels the row by its current revision, not by the lifecycle flag", () => {
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "published" })).toBe("已交付");
    // The backend word is `generating`, not `running`; both are labelled because the
    // revision list has answered both spellings during the P0 passes.
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "generating" })).toBe("生成中");
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "awaiting_outline" })).toBe(
      "待确认提纲",
    );
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "failed" })).toBe("生成失败");
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "cancelled" })).toBe("已取消");
    // A partial delivery is published but must not be flattened into 已交付.
    expect(
      reportDeliveryLabel(t, { status: "active", revision_status: "published", completeness: "partial" }),
    ).toBe("已交付（部分）");
    // No revision readable (dangling pointer) ⇒ fall back to the lifecycle flag,
    // which is the only fact left; it must not be invented into a delivery state.
    expect(reportDeliveryLabel(t, { status: "active" })).toBe("在用");
    expect(reportDeliveryLabel(t, { status: "archived" })).toBe("已归档");
    expect(reportDeliveryLabel(t, { status: "active", revision_status: "weird" })).toBe("weird");
  });

  it("speaks the reader's language for revision, section, quality and evidence", () => {
    // Every value the backend can write gets a label; an unlabelled one is the
    // defect that showed up live: `v1 · awaiting_outline`, and a section badge
    // reading `succeeded · passed · sufficient` in an otherwise Chinese UI.
    expect(reportRevisionStatusLabel(t, "awaiting_outline")).toBe("待确认提纲");
    expect(reportRevisionStatusLabel(t, "published")).toBe("已交付");
    expect(reportCompletenessLabel(t, "full")).toBe("完整");
    expect(reportCompletenessLabel(t, "partial")).toBe("部分");
    // No completeness ⇒ nothing to print, not the word "undefined".
    expect(reportCompletenessLabel(t, undefined)).toBe("");

    expect(reportSectionStatusLabel(t, "succeeded")).toBe("已生成");
    expect(reportSectionStatusLabel(t, "writing")).toBe("生成中");
    expect(reportSectionStatusLabel(t, "skipped")).toBe("已跳过");

    // Four verdicts, and `not_checked` must NOT be dressed as 通过: a section the
    // gate never reached is not a section that passed.
    expect(reportQualityStatusLabel(t, "not_checked")).toBe("未质检");
    expect(reportQualityStatusLabel(t, "needs_review")).toBe("需复核");
    expect(reportQualityStatusLabel(t, "failed")).toBe("质量未过");

    // Six evidence statuses (doc/288 §9.3) — `unsupported` is "证据不足", not
    // "章节缺失": existence is decided by section_key.
    for (const [status, label] of [
      ["not_required", "无需证据"],
      ["supported", "证据充分"],
      ["partially_supported", "证据部分支持"],
      ["unsupported", "证据不足"],
      ["conflicting", "证据冲突"],
      ["outdated", "证据过期"],
    ]) {
      expect(reportEvidenceStatusLabel(t, status)).toBe(label);
    }

    // Unknown values stay raw on every one of these maps: a fabricated label is
    // worse than an ugly one.
    expect(reportSectionStatusLabel(t, "brand_new")).toBe("brand_new");
    expect(reportQualityStatusLabel(t, "brand_new")).toBe("brand_new");
    expect(reportEvidenceStatusLabel(t, "brand_new")).toBe("brand_new");
    expect(reportRevisionStatusLabel(t, "brand_new")).toBe("brand_new");
  });
});

/**
 * P2 (doc/295) pure helpers. Each one encodes a rule that a component would
 * otherwise re-derive — and would get subtly wrong in one of two places.
 */
describe("P2 disclosures and schedules", () => {
  it("keeps only non-empty locator lines and never invents one", () => {
    expect(reportSectionLocationHints({ location_hints: ["页面索引：A", " ", ""] })).toEqual([
      "页面索引：A",
    ]);
    expect(reportSectionLocationHints({ location_hints: [] })).toEqual([]);
    // A malformed value is "no locators", not a rendering exception: the disclosure
    // is an aid, so it may never be the reason a section fails to draw.
    expect(reportSectionLocationHints({ location_hints: "页面索引：A" })).toEqual([]);
    expect(reportSectionLocationHints({})).toEqual([]);
  });

  it("treats an absent research plan as 'none' rather than zeroed", () => {
    expect(reportSectionResearchPlan({})).toBeNull();
    expect(reportSectionResearchPlan({ research_plan: null })).toBeNull();
    expect(reportSectionResearchPlan({ research_plan: { depth: "", planning: "" } })).toBeNull();
    expect(
      reportSectionResearchPlan({
        research_plan: { depth: "deep", planning: "planned", merged: 5, duplicates: 2 },
      }),
    ).toEqual({ depth: "deep", planning: "planned", merged: 5, duplicates: 2 });
  });

  it("prints a plan only when it says something a reader cannot see", () => {
    expect(shouldShowResearchPlan(null)).toBe(false);
    // The default: retrieval was not widened, so there is nothing to disclose.
    expect(shouldShowResearchPlan({ depth: "standard", planning: "standard", merged: 0, duplicates: 0 })).toBe(
      false,
    );
    // A deep run discloses even when it merged nothing — the attempt is the news.
    expect(shouldShowResearchPlan({ depth: "deep", planning: "planned", merged: 0, duplicates: 0 })).toBe(true);
    // A degradation on a standard-depth section still explains itself.
    expect(
      shouldShowResearchPlan({
        depth: "standard",
        planning: "fallback_budget",
        merged: 0,
        duplicates: 0,
      }),
    ).toBe(true);
  });

  it("words every R18 and R20 enum, and leaves unknown values raw", () => {
    expect(reportResearchDepthLabel(t, "deep")).toBe("深研检索");
    expect(reportResearchPlanningLabel(t, "fallback_budget")).toBe("深研未启用：预算不足");
    expect(reportScheduleCadenceLabel(t, "weekly")).toBe("每周");
    expect(reportScheduleUpdateModeLabel(t, "reuse_static")).toBe("保留静态章节");
    expect(reportScheduleRunStatusLabel(t, "skipped_no_base")).toBe("跳过：没有可沿用的已发布版本");
    expect(reportTemplateSourceLabel(t, "platform")).toBe("平台内置");
    expect(reportTemplateStatusLabel(t, "retired")).toBe("已退休");
    expect(reportResearchPlanningLabel(t, "brand_new")).toBe("brand_new");
    expect(reportScheduleRunStatusLabel(t, "brand_new")).toBe("brand_new");
  });

  it("formats the clock and the offset a plan runs in", () => {
    expect(formatScheduleClock(9, 5)).toBe("09:05");
    expect(formatScheduleClock(0, 0)).toBe("00:00");
    // Out-of-range input is clamped rather than printed as `24:70`.
    expect(formatScheduleClock(99, 99)).toBe("23:59");
    expect(formatTZOffset(480)).toBe("UTC+08:00");
    expect(formatTZOffset(-720)).toBe("UTC-12:00");
    expect(formatTZOffset(840)).toBe("UTC+14:00");
  });

  it("renders a schedule in its own wall clock", () => {
    const base = {
      report_id: "rep-1",
      cadence: "daily",
      hour: 21,
      minute: 30,
      weekday: 3,
      tz_offset_min: -210,
      update_mode: "full",
      enabled: true,
      next_run_at: "2026-11-25T00:30:00Z",
      last_run_at: null,
      last_revision: 0,
      last_status: "",
      last_error: "",
      created_at: "",
      updated_at: "",
    };
    expect(reportScheduleWhen(t, base)).toBe("每天 21:30（UTC-03:30）");
    expect(reportScheduleWhen(t, { ...base, cadence: "weekly" })).toBe("每周三 21:30（UTC-03:30）");
  });

  it("names a template in the reading language, falling back to its key", () => {
    const row = {
      template_id: "tpl-1",
      template_key: "analysis",
      version: 1,
      tenant_id: "t-1",
      payload: { name: { zh: "风险分析报告", en: "Risk analysis" } },
      status: "active",
      retired: false,
      source: "workspace",
      editable: true,
    };
    expect(reportTemplateName(row, "zh-CN")).toBe("风险分析报告");
    expect(reportTemplateName(row, "en")).toBe("Risk analysis");
    // A half-translated template still shows the half it has.
    expect(reportTemplateName({ ...row, payload: { name: { zh: "仅中文" } } }, "en")).toBe("仅中文");
    // No name at all: the key is honest, an empty cell is not.
    expect(reportTemplateName({ ...row, payload: {} }, "zh")).toBe("analysis");
  });

  it("says why a template offers no edit button", () => {
    const row = {
      template_id: "tpl-1",
      template_key: "analysis",
      version: 1,
      tenant_id: "t-1",
      payload: {},
      status: "active",
      retired: false,
      source: "platform",
      editable: false,
    };
    expect(reportTemplateEditBlocker(t, { ...row, editable: true })).toBeNull();
    expect(reportTemplateEditBlocker(t, row)).toBe("平台内置模板不能在工作区修改。");
    expect(reportTemplateEditBlocker(t, { ...row, source: "workspace", retired: true })).toBe(
      "该模板已退休，无法继续改版。",
    );
    expect(reportTemplateEditBlocker(t, { ...row, source: "workspace", retired: false })).toBe(
      "当前账号不能修改模板。",
    );
  });
});
