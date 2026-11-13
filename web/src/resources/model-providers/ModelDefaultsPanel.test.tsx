// ScenarioID: SC-DEFAULTMODEL-001 (spec 26 §9 自检报告 G2/G9——G12)
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelDefaultsPanel } from "./ModelDefaultsPanel";

const apiMod = vi.hoisted(() => ({
  getModelDefaults: vi.fn(),
  getModelDefaultOptions: vi.fn(),
  patchModelDefault: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    code: number;
    displayMessage: string;
    constructor(status: number, code: number, message: string) {
      super(message);
      this.status = status;
      this.code = code;
      this.displayMessage = message;
    }
  },
}));

const notifyMock = vi.hoisted(() => vi.fn());

vi.mock("../../lib/api", () => apiMod);

vi.mock("@/components/ui/select", () => ({
  Select: ({ children, value, disabled, onValueChange, ...rest }: { children: React.ReactNode; value: string; disabled?: boolean; onValueChange?: (value: string) => void }) => (
    <select
      value={value}
      disabled={disabled}
      onChange={(event) => onValueChange?.(event.target.value)}
      {...rest}
    >
      {children}
    </select>
  ),
  SelectTrigger: () => null,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SelectGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SelectLabel: () => null,
  SelectItem: ({ value, children }: { value: string; children: React.ReactNode }) => (
    <option value={value}>{children}</option>
  ),
}));

// ModelProviderList's ra-core hooks; timed by __auth below (read-only vs manage).
let __auth = { read: true, manage: true };
vi.mock("ra-core", () => ({
  useNotify: () => notifyMock,
  useTranslate: () => (key: string) => key,
  useCanAccess: ({ action }: { action: string }) => {
    return { canAccess: action === "manage" ? __auth.manage : __auth.read };
  },
}));

const chatState = {
  model_type: "chat",
  model_id: "chat-1",
  model_name: "deepseek-v4",
  provider_name: "GPUStack",
  instance_name: "gpustack-local",
  status: "ok",
  supported: true,
};
const embeddingState = {
  model_type: "embedding",
  model_id: "",
  status: "unset",
  supported: true,
};
const decisionState = {
  model_type: "decision",
  model_id: "",
  status: "unset",
  supported: false,
};
const chatOptions = [
  {
    model_id: "chat-1",
    name: "deepseek-v4",
    provider_name: "GPUStack",
    instance_name: "gpustack-local",
  },
  {
    model_id: "chat-2",
    name: "qwen-omni",
    provider_name: "GPUStack",
    instance_name: "gpustack-local",
  },
];


async function pickTreeModel(t: string): Promise<void> {
  const user = userEvent.setup();
  await user.selectOptions(screen.getByTestId("tree-provider"), "GPUStack");
  await waitFor(() =>
    expect((screen.getByTestId("tree-instance") as HTMLSelectElement).disabled).toBe(false),
  );
  await user.selectOptions(screen.getByTestId("tree-instance"), "gpustack-local");
  await waitFor(() =>
    expect((screen.getByTestId("tree-model") as HTMLSelectElement).disabled).toBe(false),
  );
  await user.selectOptions(screen.getByTestId("tree-model"), t);
  await waitFor(() =>
    expect((screen.getByTestId("tree-model") as HTMLSelectElement).value).toBe(t),
  );
}

function renderPanel() {
  return render(<ModelDefaultsPanel />);
}

describe("ModelDefaultsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    __auth = { read: true, manage: true };
    apiMod.getModelDefaults.mockResolvedValue([chatState, embeddingState, decisionState]);
    apiMod.getModelDefaultOptions.mockResolvedValue({ chat: chatOptions, embedding: [] });
  });

  it("renders nothing without read:model-default (operator/business_user)", async () => {
    __auth = { read: false, manage: false };
    const { container } = renderPanel();
    await waitFor(() => expect(container.firstChild).toBeNull());
  });

  it("loads and shows four-state rows; hides unsupported (decision) rows", async () => {
    renderPanel();
    expect(await screen.findByText("providers.default_section_title")).toBeInTheDocument();
    // chat row shows current default identity
    expect(screen.getByText(/GPUStack \/ gpustack-local \/ deepseek-v4/)).toBeInTheDocument();
    // embedding row: no candidates → hint
    expect(screen.getByText(/providers.default_no_models/)).toBeInTheDocument();
    // decision hidden (supported=false)
    expect(screen.queryByText("providers.default_type_decision")).not.toBeInTheDocument();
  });

  it("shows invalid state hint when the default references a deleted model", async () => {
    apiMod.getModelDefaults.mockResolvedValue([
      {
        model_type: "chat",
        model_id: "ghost",
        status: "invalid",
        supported: true,
        error: "默认模型已被删除",
      },
      embeddingState,
    ]);
    renderPanel();
    expect(await screen.findByText("providers.default_status_invalid")).toBeInTheDocument();
    expect(screen.getByText("providers.default_status_invalid_hint")).toBeInTheDocument();
  });

  it("forces CAS on successful set (expected_model_id from fresh default read)", async () => {
    apiMod.patchModelDefault.mockResolvedValue({
      operation_ref: "md:chat:op1",
      model_type: "chat",
      previous_model_id: "chat-1",
      observed_model_id: "chat-2",
      status: "succeeded",
    });
    renderPanel();
    await screen.findByText("providers.default_section_title");
    // Expand tree: choose provider → instance → model (leaf), then set.
    await pickTreeModel("chat-2");
    fireEvent.click(screen.getByText("providers.default_set_btn"));
    await waitFor(() =>
      expect(apiMod.patchModelDefault).toHaveBeenCalledWith({
        model_type: "chat",
        model_id: "chat-2",
        expected_model_id: "chat-1",
      }),
    );
  });

  it("notifies on CAS conflict (409/40912) and refreshes rather than swallowing", async () => {
    apiMod.patchModelDefault.mockRejectedValue(new apiMod.ApiError(409, 40912, "conflict"));
    renderPanel();
    await screen.findByText("providers.default_section_title");
    await pickTreeModel("chat-2");
    fireEvent.click(screen.getByText("providers.default_set_btn"));
    await waitFor(() =>
      expect(notifyMock).toHaveBeenCalledWith("providers.default_cas_conflict", {
        type: "warning",
      }),
    );
    await waitFor(() => expect(apiMod.getModelDefaults).toHaveBeenCalledTimes(2));
  });

  it("notifies on unknown upstream result (50257) and refreshes", async () => {
    apiMod.patchModelDefault.mockRejectedValue(new apiMod.ApiError(502, 50257, "unknown"));
    renderPanel();
    await screen.findByText("providers.default_section_title");
    await pickTreeModel("chat-2");
    fireEvent.click(screen.getByText("providers.default_set_btn"));
    await waitFor(() =>
      expect(notifyMock).toHaveBeenCalledWith("providers.default_unknown_result", {
        type: "warning",
      }),
    );
    await waitFor(() => expect(apiMod.getModelDefaults).toHaveBeenCalledTimes(2));
  });

  it("hides write controls for read-only (tenant_admin) viewers", async () => {
    __auth = { read: true, manage: false };
    renderPanel();
    await screen.findByText("providers.default_readonly_hint");
    expect(screen.queryByText("providers.default_set_btn")).not.toBeInTheDocument();
    expect(screen.queryByText("providers.default_clear_btn")).not.toBeInTheDocument();
  });
});
