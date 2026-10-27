import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIKeyCreate } from "./APIKeyCreate";

const createMock = vi.hoisted(() => vi.fn());

vi.mock("../../dataProvider", () => ({
  dataProvider: {
    create: createMock,
  },
}));

vi.mock("ra-core", () => ({
  useNavigate: () => vi.fn(),
  useNotify: () => vi.fn(),
  useTranslate: () => (key: string) => key,
}));

describe("<APIKeyCreate />", () => {
  beforeEach(() => {
    createMock.mockReset();
    createMock.mockResolvedValue({ data: { id: "key-1", secret: "raw-secret" } });
  });

  it("converts comma-separated allowlist entries for create payload", async () => {
    const user = userEvent.setup();
    render(<APIKeyCreate />);

    await user.type(screen.getByLabelText("api_keys.key_name"), "gateway");
    await user.type(screen.getByLabelText("api_keys.allowed_ips"), "127.0.0.1, 10.20.0.0/24");
    await user.click(screen.getByRole("button", { name: "api_keys.create_action" }));

    expect(createMock).toHaveBeenCalledWith("api-keys", {
      data: {
        name: "gateway",
        allowed_ips: ["127.0.0.1", "10.20.0.0/24"],
      },
    });
    expect(await screen.findByText("api_keys.secret_hint")).toBeInTheDocument();
  });
});
