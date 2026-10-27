import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ContextSourceDialog } from "./ContextSourceDialog";

const mocks = vi.hoisted(() => ({
  useNotify: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useNotify: mocks.useNotify,
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/lib/api", () => ({
  api: { get: mocks.apiGet },
  ApiError: class extends Error {
    displayMessage = "request failed";
  },
}));

describe("ContextSourceDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.apiGet.mockResolvedValue({
      data: {
        code: 0,
        data: {
          id: "decision-1",
          request_id: "run-1",
          decision: "memory_allowed",
          detail: JSON.stringify({
            reason: "",
            context_sources: ["knowledge", "memory"],
            target_kind: "agent",
            policy_scope: "user",
            policy_generation: 2,
            consent_version: 1,
            release_manifest_status: "not_managed",
            memory_retrieved: false,
            memory_result_count: 2,
            raw_payload: "must not be rendered",
          }),
        },
      },
    });
  });

  it("loads and renders safe governance metadata only", async () => {
    render(
      <ContextSourceDialog requestId="run-1" open onOpenChange={vi.fn()} />,
    );

    expect(
      await screen.findByText(/context_source_allowed/),
    ).toBeInTheDocument();
    expect(screen.getByText("knowledge、memory")).toBeInTheDocument();
    expect(
      screen.getByText("conversationCenter.target_kind_agent"),
    ).toBeInTheDocument();
    expect(screen.getByText("user")).toBeInTheDocument();
    expect(screen.getAllByText("false").length).toBeGreaterThan(0);
    expect(screen.getAllByText("2").length).toBeGreaterThan(0);
    expect(screen.queryByText("must not be rendered")).not.toBeInTheDocument();
    expect(mocks.apiGet).toHaveBeenCalledWith(
      "/conversation-context/decisions/by-request/run-1",
    );
  });

  it("renders an empty state for missing decisions", async () => {
    mocks.apiGet.mockRejectedValue(
      Object.assign(new Error("failed"), { displayMessage: "failed" }),
    );
    render(
      <ContextSourceDialog requestId="missing" open onOpenChange={vi.fn()} />,
    );

    await screen.findByText("conversationCenter.context_source_missing");
    expect(mocks.notify).toHaveBeenCalledWith(
      "conversationCenter.context_source_failed",
      { type: "error" },
    );
  });
});
