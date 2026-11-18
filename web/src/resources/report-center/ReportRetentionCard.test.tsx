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
  "reportCenter.retention_days": "%{days} 天",
  "reportCenter.retention_inherited": "（跟随系统）",
  "reportCenter.retention_fail": "保留期读取或保存失败。",
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
    // Every clock inherits: five "(system)" markers, no edit button for a reader.
    expect(screen.getAllByText("（跟随系统）")).toHaveLength(5);
    expect(screen.queryByRole("button", { name: "reportCenter.retention_edit" })).toBeNull();
  });

  it("pins only the fields the admin filled in", async () => {
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "reportCenter.retention_edit" }));
    fireEvent.change(screen.getByTestId("retention-report_days"), { target: { value: "180" } });
    fireEvent.change(screen.getByTestId("retention-report_cleanup_hold_days"), { target: { value: "0" } });
    await user.click(screen.getByRole("button", { name: "reportCenter.retention_save" }));

    // An untouched clock is omitted, not echoed: sending 90 back would pin it and a
    // later system change would silently stop applying to this workspace.
    await waitFor(() =>
      expect(mocks.apiPut).toHaveBeenCalledWith("/report-retention", {
        report_days: 180,
        report_cleanup_hold_days: 0,
      }),
    );
    // The answer is the server's resolved view, so the card never re-derives it.
    expect((await screen.findByTestId("retention-value-report_days")).textContent).toContain("180");
  });

  it("seeds the editor from the pinned fields only", async () => {
    mocks.apiGet.mockResolvedValue({ data: { code: 0, data: pinned } });
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "reportCenter.retention_edit" }));
    expect((screen.getByTestId("retention-report_days") as HTMLInputElement).value).toBe("180");
    // A zero-day window is a real choice, not "empty": it must seed as "0".
    expect((screen.getByTestId("retention-report_cleanup_hold_days") as HTMLInputElement).value).toBe("0");
    expect((screen.getByTestId("retention-report_event_days") as HTMLInputElement).value).toBe("");
  });

  it("refuses an out-of-range value before the round trip", async () => {
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "reportCenter.retention_edit" }));
    fireEvent.change(screen.getByTestId("retention-report_cleanup_hold_days"), {
      target: { value: "400" },
    });
    await user.click(screen.getByRole("button", { name: "reportCenter.retention_save" }));

    expect((await screen.findByTestId("retention-error")).textContent).toBeTruthy();
    // The server bounds are mirrored, so an impossible window never leaves the page.
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("surfaces the server's refusal and keeps the editor open", async () => {
    mocks.apiPut.mockRejectedValue(new Error("boom"));
    render(<ReportRetentionCard canManage={true} />);
    const user = userEvent.setup();
    await screen.findByTestId("retention-value-report_days");

    await user.click(screen.getByRole("button", { name: "reportCenter.retention_edit" }));
    fireEvent.change(screen.getByTestId("retention-report_days"), { target: { value: "120" } });
    await user.click(screen.getByRole("button", { name: "reportCenter.retention_save" }));

    expect((await screen.findByTestId("retention-error")).textContent).toContain("保留期读取或保存失败。");
    // Still open, still holding the admin's number.
    expect((screen.getByTestId("retention-report_days") as HTMLInputElement).value).toBe("120");
  });
});
