import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportBudgetExtensionCard } from "./ReportBudgetExtensionCard";

const mocks = vi.hoisted(() => ({
  apiPost: vi.fn(),
}));

const DICT: Record<string, string> = {
  "reportCenter.budget_extension_title": "预算追加",
  "reportCenter.budget_extension_unknown": "当前没有在跑的运行，无需追加预算。",
  "reportCenter.budget_extension_envelope": "当前额度 %{budget} tokens，单次最多追加 %{max} tokens。",
  "reportCenter.budget_extension_observe": "本工作区是「只计量不拦截」模式。",
  "reportCenter.budget_extension_request": "申请追加",
  "reportCenter.budget_extension_tokens": "追加额度（tokens）",
  "reportCenter.budget_extension_reason": "追加原因",
  "reportCenter.budget_extension_rule": "追加只增加「还能花多少」。",
  "reportCenter.budget_extension_send": "提交申请",
  "reportCenter.budget_extension_result": "%{status}：本次追加 %{added} tokens，当前额度 %{budget} tokens。",
  "reportCenter.budget_extension_pending_hint": "审批通过后额度才会生效。",
  "reportCenter.budget_extension_tokens_invalid": "追加额度必须是正整数。",
  "reportCenter.budget_extension_fail": "预算追加申请失败。",
  "reportCenter.label_budget_extension.applied": "已追加",
  "reportCenter.label_budget_extension.pending_approval": "等待审批",
  "ra.action.cancel": "取消",
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
  api: { post: mocks.apiPost },
  ApiError: class extends Error {
    readonly displayMessage = "追加额度超过上限（409/44104）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

describe("ReportBudgetExtensionCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.apiPost.mockResolvedValue({
      data: {
        code: 0,
        data: { status: "applied", added_tokens: 100, budget_tokens: 5100 },
      },
    });
  });

  it("states the envelope and the bound before anything is asked", () => {
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={true} />);

    // "At most the current envelope again" is the server's rule; the card quotes it
    // so the request it offers cannot be one the service will refuse.
    expect(screen.getByText("当前额度 5000 tokens，单次最多追加 5000 tokens。")).toBeTruthy();
    expect(screen.queryByTestId("report-budget-extension-tokens")).toBeNull();
  });

  it("offers nothing when there is no live run", () => {
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={null} canExecute={true} />);

    expect(screen.getByText("当前没有在跑的运行，无需追加预算。")).toBeTruthy();
    expect(screen.queryByTestId("report-budget-extension-open")).toBeNull();
  });

  it("says nothing about the envelope when the caller may not execute", () => {
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={false} />);

    expect(screen.queryByTestId("report-budget-extension-open")).toBeNull();
  });

  it("explains an observe workspace instead of hiding the case", () => {
    render(
      <ReportBudgetExtensionCard reportId="rep-1" budget={5000} budgetPolicy="observe" canExecute={true} />,
    );

    expect(screen.getByText(/只计量不拦截/)).toBeTruthy();
  });

  it("submits the increment and reports it as applied", async () => {
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={true} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("report-budget-extension-open"));
    fireEvent.change(screen.getByTestId("report-budget-extension-tokens"), {
      target: { value: "100" },
    });
    fireEvent.change(screen.getByTestId("report-budget-extension-reason"), {
      target: { value: "补齐第三章" },
    });
    await user.click(screen.getByTestId("report-budget-extension-submit"));

    await waitFor(() =>
      expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/budget-extension", {
        tokens: 100,
        reason: "补齐第三章",
      }),
    );
    const result = await screen.findByTestId("report-budget-extension-result");
    expect(result.dataset.status).toBe("applied");
    expect(result.textContent).toContain("已追加");
    expect(result.textContent).toContain("5100");
  });

  it("does not dress a pending approval as a raised limit", async () => {
    mocks.apiPost.mockResolvedValue({
      data: {
        code: 0,
        data: {
          status: "pending_approval",
          added_tokens: 100,
          budget_tokens: 5000,
          approval_id: "ap-1",
        },
      },
    });
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={true} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("report-budget-extension-open"));
    fireEvent.change(screen.getByTestId("report-budget-extension-tokens"), {
      target: { value: "100" },
    });
    await user.click(screen.getByTestId("report-budget-extension-submit"));

    const result = await screen.findByTestId("report-budget-extension-result");
    expect(result.dataset.status).toBe("pending_approval");
    expect(result.textContent).toContain("等待审批");
    expect(result.textContent).not.toContain("已追加");
    // The envelope did not move: 5000, not 5100.
    expect(result.textContent).toContain("5000");
    expect(result.textContent).toContain("审批通过后额度才会生效。");
  });

  it("refuses a zero increment without a round trip", async () => {
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={true} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("report-budget-extension-open"));
    fireEvent.change(screen.getByTestId("report-budget-extension-tokens"), {
      target: { value: "0" },
    });
    await user.click(screen.getByTestId("report-budget-extension-submit"));

    expect((await screen.findByTestId("report-budget-extension-error")).textContent).toBe(
      "追加额度必须是正整数。",
    );
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });

  it("shows the server's refusal when the request is out of bounds", async () => {
    // The service refuses an over-large increment with its own sentence; the card
    // prints that sentence, not the fallback "request failed".
    const { ApiError } = await import("../../lib/api");
    mocks.apiPost.mockRejectedValue(new ApiError(409, 44104, "budget over"))
    render(<ReportBudgetExtensionCard reportId="rep-1" budget={5000} canExecute={true} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("report-budget-extension-open"));
    fireEvent.change(screen.getByTestId("report-budget-extension-tokens"), {
      target: { value: "9999" },
    });
    await user.click(screen.getByTestId("report-budget-extension-submit"));

    expect((await screen.findByTestId("report-budget-extension-error")).textContent).toBe(
      "追加额度超过上限（409/44104）",
    );
    expect(screen.queryByTestId("report-budget-extension-result")).toBeNull();
  });
});
