import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { api } from "@/lib/api";
import { ConnectorRunsPanel } from "./ConnectorRunsPanel";

const t = (key: string) => key;

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () => t,
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  api: { get: vi.fn() },
}));

const get = vi.mocked(api.get);

describe("ConnectorRunsPanel", () => {
  beforeEach(() => {
    get.mockReset();
  });

  it("shows the triggering actor and lazily loads per-object skip reasons", async () => {
    get
      .mockResolvedValueOnce({
        data: {
          code: 0,
          data: {
            items: [
              {
                id: "run-1",
                trigger: "manual",
                triggered_by: "u-operator",
                status: "failed",
                skipped: 1,
                failed: 1,
                error_summary: "one object failed",
              },
            ],
            total: 1,
          },
        },
      })
      .mockResolvedValueOnce({
        data: {
          code: 0,
          data: {
            id: "run-1",
            detail: {
              skipped: [{ key: "huge.bin", reason: "large", size: 26 << 20 }],
              skipped_unchanged: 2,
            },
          },
        },
      });

    render(<ConnectorRunsPanel connectorId="conn-1" />);

    expect(await screen.findByText("u-operator")).toBeInTheDocument();
    expect(screen.getByText("one object failed")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole("button", { name: "connectors.runs.show_details" }));

    expect(await screen.findByText("huge.bin")).toBeInTheDocument();
    expect(screen.getByText(/26 MiB/)).toBeInTheDocument();
    expect(get).toHaveBeenLastCalledWith("/connectors/conn-1/runs/run-1");
  });
});
