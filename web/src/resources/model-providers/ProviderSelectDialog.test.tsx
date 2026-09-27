import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProviderSelectDialog } from "./ProviderSelectDialog";

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api: apiMock,
  ApiError: class extends Error {
    displayMessage = "api error";
  },
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useCanAccess: () => ({ canAccess: false }),
  useGetIdentity: async () => ({ id: "user-1", tenant_id: "tenant-1", role: "tenant_admin" }),
  useGetList: () => ({ data: [] }),
}));

vi.mock("../approvals/ApprovalHoldDialog", () => ({
  ApprovalHoldDialog: () => null,
}));

vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children, open }: { children?: React.ReactNode; open: boolean }) =>
    open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children?: React.ReactNode }) => <p>{children}</p>,
  DialogHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children?: React.ReactNode }) => <h2>{children}</h2>,
}));

vi.mock("@/components/ui/button", () => ({
  Button: ({ children, onClick, variant }: any) => (
    <button onClick={onClick} data-variant={variant}>{children}</button>
  ),
}));

vi.mock("@/components/ui/input", () => ({
  Input: (props: any) => <input {...props} />,
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: any) => <span>{children}</span>,
}));

vi.mock("@/components/ui/skeleton", () => ({
  Skeleton: () => <div data-testid="provider-skeleton" />,
}));

describe("ProviderSelectDialog", () => {
  beforeEach(() => {
    apiMock.get.mockReset();
    apiMock.post.mockReset();
  });

  it("sorts private-deployment-friendly providers first and creates a provider with an idempotency key", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const onAdded = vi.fn();
    apiMock.get.mockResolvedValue({
      status: 200,
      data: {
        code: 0,
        data: [
          { name: "Azure-OpenAI", model_types: ["llm"] },
          { name: "OpenAI-API-Compatible", model_types: ["llm", "embedding"] },
          { name: "Ollama", default_url: "http://localhost:11434", model_types: ["llm"] },
        ],
      },
    });
    apiMock.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });

    render(
      <ProviderSelectDialog open onOpenChange={onOpenChange} onAdded={onAdded} />,
    );

    const [openAIButton, ollamaButton, azureButton] = await screen.findAllByRole(
      "button",
      { name: /OpenAI|Ollama|Azure/ },
    );
    expect(openAIButton.textContent).toMatch(/^OpenAI Compatible/);
    expect(ollamaButton.textContent).toMatch(/^Ollama/);
    expect(azureButton.textContent).toMatch(/^Azure OpenAI/);

    await user.click(screen.getByRole("button", { name: /Ollama/ }));
    expect(apiMock.post).toHaveBeenCalledWith(
      "/model-providers",
      { provider_name: "Ollama" },
      { headers: { "Idempotency-Key": expect.any(String) } },
    );
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onAdded).toHaveBeenCalledTimes(1);
  });

  it("filters providers by brand or catalog name", async () => {
    const user = userEvent.setup();
    apiMock.get.mockResolvedValue({
      status: 200,
      data: {
        code: 0,
        data: [
          { name: "Azure-OpenAI" },
          { name: "OpenAI-API-Compatible" },
          { name: "Ollama" },
        ],
      },
    });

    render(
      <ProviderSelectDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />,
    );

    expect(await screen.findByRole("button", { name: /OpenAI Compatible/ })).toBeInTheDocument();
    await user.type(screen.getByPlaceholderText("providers.search_placeholder"), "ollama");
    expect(screen.getByRole("button", { name: /Ollama/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Azure OpenAI/ })).not.toBeInTheDocument();
  });
});
