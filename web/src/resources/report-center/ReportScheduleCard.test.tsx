/**
 * R20's schedule card (doc/295 §2.4, A60–A62).
 *
 * The assertions follow the three things the card promises to state honestly:
 *  - the next run comes from the server's `next_run_at`, never from the browser's
 *    arithmetic over `cadence` + `hour` (the client does not know the plan's offset);
 *  - a `skipped_*` status is *not* a failure — the four skip reasons are closed-set
 *    answers, each with a different next step, so none may render as "运行失败";
 *  - "no schedule" is `{"schedule": null}` with 200, i.e. a normal answer that
 *    offers the creation form rather than an error banner.
 */
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportScheduleCard, schedulePatch } from "./ReportScheduleCard";
import { formatTZOffset, type ReportScheduleView } from "./report-center-api";
import { tZh } from "../../test/translate";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
  apiDelete: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => tZh,
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, put: mocks.apiPut, delete: mocks.apiDelete },
  ApiError: class extends Error {
    readonly displayMessage = "计划参数不合法（400/44105）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

const view: ReportScheduleView = {
  report_id: "rep-1",
  cadence: "weekly",
  hour: 9,
  minute: 5,
  weekday: 3,
  tz_offset_min: 480,
  update_mode: "reuse_static",
  enabled: true,
  next_run_at: "2026-11-25T01:05:00Z",
  last_run_at: "2026-11-18T01:05:00Z",
  last_revision: 7,
  last_status: "created",
  last_error: "",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-18T01:05:00Z",
};

/** The drawer formats absolute times; the card only asks for a string. */
const formatTime = (value: string | null | undefined) => (value ? `T<${value}>` : "—");

function ok(data: unknown) {
  return Promise.resolve({ data: { code: 0, data } });
}

describe("ReportScheduleCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("offers the creation form when the report has no plan", async () => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: null }));
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    // `{"schedule": null}` is a normal answer, so no error surface appears.
    expect(await screen.findByTestId("report-schedule-save")).toBeTruthy();
    expect(screen.getByText("布防")).toBeTruthy();
    expect(screen.queryByTestId("report-schedule-error")).toBeNull();
    expect(screen.queryByTestId("report-schedule-delete")).toBeNull();
  });

  it("states when, next run and rebuild scope from the server's plan", async () => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: view }));
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    expect(await screen.findByTestId("report-schedule-when")).toBeTruthy();
    // Wall clock + fixed offset: 每周三 09:05（UTC+08:00）.
    expect(screen.getByTestId("report-schedule-when").textContent).toBe("每周三 09:05（UTC+08:00）");
    // The next run is the server's UTC timestamp, formatted, not a local guess.
    expect(screen.getByTestId("report-schedule-next").textContent).toBe("T<2026-11-25T01:05:00Z>");
    expect(screen.getByTestId("report-schedule-mode").textContent).toBe("保留静态章节");
    expect(screen.getByTestId("report-schedule-last").textContent).toContain("已生成新版本");
    expect(screen.getByTestId("report-schedule-last").textContent).toContain("（第 7 版）");
    // Raw enum values never reach the screen.
    expect(screen.queryByText("reuse_static")).toBeNull();
    expect(screen.queryByText("created")).toBeNull();
  });

  it.each([
    ["skipped_busy", "跳过：已有任务在跑"],
    ["skipped_no_base", "跳过：没有可沿用的已发布版本"],
    ["skipped_template_missing", "跳过：模板已不可用"],
    ["skipped_disabled", "跳过：计划已停用"],
  ])("reads %s as its own reason, never as a failure", async (status, expected) => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: { ...view, last_status: status, last_error: "" } }));
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    const last = await screen.findByTestId("report-schedule-last");
    expect(last.textContent).toContain(expected);
    expect(last.textContent).not.toContain("运行失败");
    // A skip carries no error text, so the failure block stays hidden.
    expect(screen.queryByTestId("report-schedule-last-error")).toBeNull();
  });

  it("shows a real failure's reason", async () => {
    mocks.apiGet.mockImplementation(async () =>
      ok({ schedule: { ...view, last_status: "failed", last_error: "provider timeout" } }),
    );
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    expect((await screen.findByTestId("report-schedule-last")).textContent).toContain("运行失败");
    expect(screen.getByTestId("report-schedule-last-error").textContent).toBe("provider timeout");
  });

  it("saves the plan with the browser's offset on the first write", async () => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: null }));
    mocks.apiPut.mockImplementation(async () => ok({ schedule: { ...view, cadence: "daily", weekday: 1 } }));
    const user = userEvent.setup();
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    await user.click(await screen.findByTestId("report-schedule-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPut.mock.calls[0];
    expect(url).toBe("/reports/rep-1/schedule");
    // A first save must carry an offset: the server would otherwise silently pick
    // its own default for a workspace that is not there.
    expect(body.tz_offset_min).toBe(-new Date().getTimezoneOffset());
    expect(body.cadence).toBe("daily");
    expect(body.hour).toBe(9);
    expect(body.minute).toBe(0);
    // The response is re-rendered from the server's row, not from the draft.
    expect(await screen.findByTestId("report-schedule-when")).toBeTruthy();
  });

  it("does not resend the offset once the plan exists", () => {
    const patch = schedulePatch(
      { cadence: "daily", hour: "9", minute: "0", weekday: "1", updateMode: "full", enabled: true },
      view,
    );
    expect(patch.tz_offset_min).toBeUndefined();
  });

  it("refuses an out-of-range field before the round trip", () => {
    expect(() =>
      schedulePatch(
        { cadence: "daily", hour: "24", minute: "0", weekday: "1", updateMode: "full", enabled: true },
        view,
      ),
    ).toThrow(RangeError);
    expect(() =>
      schedulePatch(
        { cadence: "weekly", hour: "9", minute: "0", weekday: "7", updateMode: "full", enabled: true },
        null,
      ),
    ).toThrow(RangeError);
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("deletes the plan and falls back to the creation form", async () => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: view }));
    mocks.apiDelete.mockImplementation(async () => ({ data: { code: 0, data: null } }));
    const user = userEvent.setup();
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    await user.click(await screen.findByTestId("report-schedule-delete"));

    await waitFor(() => expect(mocks.apiDelete).toHaveBeenCalledWith("/reports/rep-1/schedule"));
    // 204 either way, so the card does not read a body: it just shows "no plan".
    expect(await screen.findByText("布防")).toBeTruthy();
    expect(screen.queryByTestId("report-schedule-when")).toBeNull();
  });

  it("hides the form from a caller without execute:report but still shows the plan", async () => {
    mocks.apiGet.mockImplementation(async () => ok({ schedule: view }));
    render(<ReportScheduleCard reportId="rep-1" canExecute={false} formatTime={formatTime} />);

    expect(await screen.findByTestId("report-schedule-when")).toBeTruthy();
    expect(screen.queryByTestId("report-schedule-save")).toBeNull();
    expect(screen.queryByTestId("report-schedule-delete")).toBeNull();
  });

  it("renders a failed read as a sentence, not an exception", async () => {
    mocks.apiGet.mockResolvedValue({ data: { code: 1, data: null } });
    render(<ReportScheduleCard reportId="rep-1" canExecute formatTime={formatTime} />);

    expect((await screen.findByTestId("report-schedule-error")).textContent).toBe("定时计划读取失败。");
  });

  it("formats negative and half-hour offsets", () => {
    expect(formatTZOffset(480)).toBe("UTC+08:00");
    expect(formatTZOffset(-210)).toBe("UTC-03:30");
    expect(formatTZOffset(0)).toBe("UTC+00:00");
  });
});
