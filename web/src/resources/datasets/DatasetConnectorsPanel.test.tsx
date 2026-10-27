import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { DatasetConnectorsPanel } from "./DatasetConnectorsPanel";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  t: (key: string) => key,
}));

vi.mock("ra-core", () => ({
  useRecordContext: () => ({ id: "dataset-1" }),
  useTranslate: () => mocks.t,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: { get: mocks.apiGet },
  };
});

describe("DatasetConnectorsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("hides the panel when the caller cannot read connectors (403)", async () => {
    mocks.apiGet.mockRejectedValueOnce(new ApiError(403, 40301, "forbidden"));
    const { container } = render(<DatasetConnectorsPanel />);
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a real load error and recovers through retry", async () => {
    const user = userEvent.setup();
    mocks.apiGet.mockRejectedValue(new Error("network down"));

    render(
      <MemoryRouter>
        <DatasetConnectorsPanel />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("connectors.dataset_load_failed");

    mocks.apiGet.mockResolvedValueOnce({
      data: {
        data: { items: [{ id: "co-1", name: "nightly-import", provider: "s3" }], total: 1 },
      },
    });
    await user.click(screen.getByRole("button", { name: "connectors.retry" }));
    expect(await screen.findByText("nightly-import")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("surfaces a non-403 ApiError message", async () => {
    const err = new ApiError(500, 50000, "upstream exploded");
    mocks.apiGet.mockRejectedValue(err);
    render(
      <MemoryRouter>
        <DatasetConnectorsPanel />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(err.displayMessage);
  });
});
