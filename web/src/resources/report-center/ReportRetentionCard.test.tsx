import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportRetentionCard } from "./ReportRetentionCard";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
}));

// Only the strings this card renders are resolved; every other key stays a key, so
// a test can never pass by matching copy that moved somewhere else.
const DICT: Record<string, string> = {
  "reportCenter.retention_title": "报告保留期",
  "reportCenter.retention_edit": "调整保留期",
  "reportCenter.retention_save": "保存",
  "reportCenter.retention_days": "%{days} 天",
  "reportCenter.retention_inherited": "（跟随系统）",
  "reportCenter.retention_fail": "保留期读取或保存失败。",
  "reportCenter.retention_blank_means_inherit": "留空 = 跟随系统值；填了就用这里的值，系统以后怎么改都不再影响本工作区。",
  "reportCenter.retention_budget_policy": "预算开关",
  "reportCenter.retention_auto_confirm_policy": "提纲自动确认",
  "reportCenter.retention_switch_inherit": "跟随系统（%{value}）",
  "reportCenter.retention_switch_hint": "两个开关只在新建报告时读取并固化到该次运行上。",
  // The enum words come from the shared dictionary; the double resolves the two this
  // card renders.
  "reportCenter.label_budget_policy.enforce": "硬性限额",
  "reportCenter.label_budget_policy.observe": "只计量不拦截",
  "reportCenter.label_auto_confirm_policy.self": "负责人自行决定",
  "reportCenter.label_auto_confirm_policy.approval": "需审批",
};

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string, options?: Record<string, string>) => {
    const template = DICT[key] ?? key;
    if (!options) return template;
    return Object.entries(options).reduce(
      (text, [name, value]) => text.split(`%{${name}}`).join(value),
      template,
    );
  },
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, put: mocks.apiPut },
  ApiError: class extends Error {
    readonly displayMessage = "保留期无效（400/44105）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

const inherited = {
  tenant_id: "t-1",
  report_days: 90,
  report_evidence_days: 90,
  report_artifact_days: 30,
  report_event_days: 7,
  report_cleanup_hold_days: 7,
  system_report_days: 90,
  system_report_evidence_days: 90,
  system_report_artifact_days: 30,
  system_report_event_days: 7,
  system_report_cleanup_hold_days: 7,
  budget_policy: "enforce",
  auto_confirm_policy: "self",
  system_budget_policy: "enforce",
  system_auto_confirm_policy: "self",
  overridden: [] as string[],
};

const pinned = {
  ...inherited,
  report_days: 180,
  report_cleanup_hold_days: 0,
  overridden: ["report_days", "report_cleanup_hold_days"],
  updated_by: "u-admin",
  updated_at: "2026-11-18T10:00:00Z",
};

describe("ReportRetentionCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.apiGet.mockResolvedValue({ data: { code: 0, data: inherited } });
    mocks.apiPut.mockResolvedValue({ data: { code: 0, data: pinned } });
  });

  it("renders the effective clocks and marks the inherited ones", async () => {
    render(<ReportRetentionCard canManage={false} />);

    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/report-retention"));
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalled());
    // The double echoes keys, so the day count is asserted on the rendered value.
    expect((await screen.findByTestId("retention-value-report_days")).textContent).toContain("90");
    // Every clock *and* both switches inherit: seven "(system)" markers, no edit
    // button for a reader.
    expect(screen.getAllByText("（跟随系统）")).toHaveLength(7);
    expect(screen.queryByRole("button", { name: "调整保留期" })).toBeNull();
  });

  it("pins only the fields the admin filled in", async () => {
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "调整保留期" }));
    fireEvent.change(screen.getByTestId("retention-report_days"), { target: { value: "180" } });
    fireEvent.change(screen.getByTestId("retention-report_cleanup_hold_days"), { target: { value: "0" } });
    await user.click(screen.getByRole("button", { name: "保存" }));

    // An untouched clock is omitted, not echoed: sending 90 back would pin it and a
    // later system change would silently stop applying to this workspace.
    await waitFor(() =>
      expect(mocks.apiPut).toHaveBeenCalledWith("/report-retention", {
        report_days: 180,
        report_cleanup_hold_days: 0,
        // Both switches always travel: an omitted one is indistinguishable from
        // "inherit" on this endpoint, and the card must be able to revert a pinned
        // switch back to following the system.
        budget_policy: "",
        auto_confirm_policy: "",
      }),
    );
    // The answer is the server's resolved view, so the card never re-derives it.
    expect((await screen.findByTestId("retention-value-report_days")).textContent).toContain("180");
  });

  it("writes a switch only when the admin picked one", async () => {
    mocks.apiGet.mockResolvedValue({ data: { code: 0, data: inherited } });
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "调整保留期" }));
    // The closed set is the server's: the select offers exactly the two policies, and
    // the answer is the *word*, never the raw value.
    const select = screen.getByTestId("retention-switch-input-budget_policy") as HTMLSelectElement;
    expect(Array.from(select.options).map((option) => option.value)).toEqual([
      "",
      "enforce",
      "observe",
    ]);
    fireEvent.change(select, { target: { value: "observe" } });
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() =>
      expect(mocks.apiPut).toHaveBeenCalledWith("/report-retention", {
        budget_policy: "observe",
        auto_confirm_policy: "",
      }),
    );
  });

  it("seeds the editor from the pinned fields only", async () => {
    mocks.apiGet.mockResolvedValue({ data: { code: 0, data: pinned } });
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "调整保留期" }));
    expect((screen.getByTestId("retention-report_days") as HTMLInputElement).value).toBe("180");
    // A zero-day window is a real choice, not "empty": it must seed as "0".
    expect((screen.getByTestId("retention-report_cleanup_hold_days") as HTMLInputElement).value).toBe("0");
    expect((screen.getByTestId("retention-report_event_days") as HTMLInputElement).value).toBe("");
  });

  it("refuses an out-of-range value before the round trip", async () => {
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "调整保留期" }));
    fireEvent.change(screen.getByTestId("retention-report_cleanup_hold_days"), {
      target: { value: "400" },
    });
    await user.click(screen.getByRole("button", { name: "保存" }));

    expect((await screen.findByTestId("retention-error")).textContent).toBeTruthy();
    // The server bounds are mirrored, so an impossible window never leaves the page.
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("surfaces the server's refusal and keeps the editor open", async () => {
    mocks.apiPut.mockRejectedValue(new Error("boom"));
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "调整保留期" }));
    fireEvent.change(screen.getByTestId("retention-report_days"), { target: { value: "120" } });
    await user.click(screen.getByRole("button", { name: "保存" }));

    expect((await screen.findByTestId("retention-error")).textContent).toContain("保留期读取或保存失败。");
    // Still open, still holding the admin's number.
    expect((screen.getByTestId("retention-report_days") as HTMLInputElement).value).toBe("120");
  });
});
