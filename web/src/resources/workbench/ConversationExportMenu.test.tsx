import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConversationExportMenu } from "./ConversationExportMenu";
import type { Message } from "./workbench-types";

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

import { api } from "../../lib/api";

const messages: Message[] = [
  { id: "m1", role: "user", content: "question", createdAt: "2026-09-22T01:00:00.000Z" },
  { id: "m2", role: "assistant", content: "answer", turnId: "assistant-run-1", createdAt: "2026-09-22T01:00:01.000Z" },
];

describe("<ConversationExportMenu />", () => {
  it("exports the full conversation through the formal PDF pipeline", async () => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.post).mockReset();
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === "/answer-snapshots/by-request/run-1") {
        return { data: { code: 0, data: { answer_snapshot: { id: "snapshot-1" } } } } as never;
      }
      return { data: new Blob(["pdf"], { type: "application/pdf" }) } as never;
    });
    vi.mocked(api.post).mockResolvedValue({
      data: { code: 0, data: { export_artifact: { id: "artifact-1", filename: "conversation.pdf" } } },
    } as never);
    const created = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:test");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    render(<ConversationExportMenu messages={messages} title="conversation" />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "conversation.export" }));
    await user.click(await screen.findByText("PDF"));

    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/answer-snapshots/snapshot-1/export",
      { format: "pdf", scope: "conversation" },
    ));

    created.mockRestore();
    revoke.mockRestore();
    anchorClick.mockRestore();
  });

  // doc/118 F-14: a >256KiB export returns QUEUED without an artifact. The
  // menu must poll the export job endpoint until the job reports SUCCEEDED
  // and then download the artifact. Real timers are used: the first poll
  // happens after the component's 2s interval, well inside the test timeout.
  it("polls a queued export job until it succeeds, then downloads", async () => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.post).mockReset();
    const created = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:test");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url === "/answer-snapshots/by-request/run-1") {
        return { data: { code: 0, data: { answer_snapshot: { id: "snapshot-queued" } } } } as never;
      }
      if (url.startsWith("/answer-snapshots/export-jobs/")) {
        return {
          data: {
            code: 0,
            data: {
              export_job: { id: "job-1", status: "SUCCEEDED" },
              export_artifact: { id: "artifact-queued", filename: "conversation.pdf" },
            },
          },
        } as never;
      }
      return { data: new Blob(["pdf"], { type: "application/pdf" }) } as never;
    });
    vi.mocked(api.post).mockResolvedValue({
      data: { code: 0, data: { export_job: { id: "job-1", status: "QUEUED" } } },
    } as never);

    render(<ConversationExportMenu messages={messages} title="conversation" />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "conversation.export" }));
    await user.click(await screen.findByText("PDF"));

    await waitFor(() => expect(api.get).toHaveBeenCalledWith("/answer-snapshots/export-jobs/job-1"), { timeout: 10_000 });
    await waitFor(() => expect(api.get).toHaveBeenCalledWith(
      "/answer-snapshots/artifacts/artifact-queued/download",
      { responseType: "blob" },
    ));

    created.mockRestore();
    revoke.mockRestore();
    anchorClick.mockRestore();
  }, 15_000);
});
