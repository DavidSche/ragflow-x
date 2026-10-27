import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryPrivacyExemptionPanel } from "./MemoryPrivacyExemptionPanel";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  useNotify: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
  apiPut: vi.fn(),
  apiDelete: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useCanAccess: mocks.useCanAccess,
  useNotify: mocks.useNotify,
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/lib/api", () => ({
  api: {
    get: mocks.apiGet,
    put: mocks.apiPut,
    delete: mocks.apiDelete,
  },
  ApiError: class extends Error {
    displayMessage = "request failed";
  },
  ApiEnvelope: class {},
  unwrap: (promise: Promise<{ data: { code: number; data: unknown } }>) =>
    promise.then((response) => response.data.data),
}));

describe("MemoryPrivacyExemptionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.apiGet.mockResolvedValue({
      data: {
        code: 0,
        data: [
          {
            id: "exemption-1",
            label: "phone_cn",
            pattern: "^ORD-\\d+$",
            rule_version: "memory-pii-rules-v1",
            version: 1,
            active: true,
            created_by: "admin",
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
      },
    });
    mocks.apiDelete.mockResolvedValue({ data: { code: 0, data: { ok: true } } });
  });

  it("hides entirely without manage @ memory-policy", async () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: false });
    const { container } = render(<MemoryPrivacyExemptionPanel />);
    await waitFor(() => {
      expect(mocks.apiGet).not.toHaveBeenCalled();
    });
    expect(container).toBeEmptyDOMElement();
  });

  it("lists exemptions and creates a new one", async () => {
    mocks.apiPut.mockResolvedValue({ data: { code: 0, data: { id: "exemption-2" } } });
    render(<MemoryPrivacyExemptionPanel />);
    await waitFor(() => {
      expect(screen.getByText("phone_cn")).toBeInTheDocument();
    });
    expect(screen.getByText("memory-pii-rules-v1")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText("memoryPrivacy.label"), "phone_cn");
    fireEvent.change(screen.getByLabelText("memoryPrivacy.pattern"), { target: { value: "ORD-[0-9]+" } });
    await user.click(screen.getByRole("button", { name: "memoryPrivacy.create" }));
    await waitFor(() => {
      expect(mocks.apiPut).toHaveBeenCalledWith("/memory-privacy/pii-exemptions", {
        label: "phone_cn",
        pattern: "ORD-[0-9]+",
      });
    });
    expect(mocks.notify).toHaveBeenCalledWith("memoryPrivacy.created", { type: "success" });
  });

  it("shows high-risk labels as not exemptable and deletes with confirmation", async () => {
    render(<MemoryPrivacyExemptionPanel />);
    await waitFor(() => {
      expect(screen.getByText("api_key")).toBeInTheDocument();
    });
    expect(screen.getByText("jwt")).toBeInTheDocument();
    expect(screen.getByText("private_key_pem")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "memoryPrivacy.delete" }));
    const dialog = await screen.findByRole("dialog");
    const confirmButtons = await within(dialog).findAllByRole("button", { name: "memoryPrivacy.delete" });
    await user.click(confirmButtons[0]);
    await waitFor(() => {
      expect(mocks.apiDelete).toHaveBeenCalledWith("/memory-privacy/pii-exemptions/exemption-1");
    });
    expect(mocks.notify).toHaveBeenCalledWith("memoryPrivacy.deleted", { type: "success" });
  });
});
