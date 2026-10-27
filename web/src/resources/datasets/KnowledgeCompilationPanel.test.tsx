import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KnowledgeCompilationPanel } from "./KnowledgeCompilationPanel";

const mocks = vi.hoisted(() => ({
  api: { get: vi.fn(), post: vi.fn() },
  notify: vi.fn(),
  access: { read: true, manage: false, execute: false } as Record<string, boolean>,
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {
    displayMessage = "api error";
  },
  api: mocks.api,
  unwrap: async <T,>(request: Promise<{ data: { data: T } }>) => (await request).data.data,
}));

vi.mock("ra-core", () => ({
  useCanAccess: ({ action }: { action: string }) => ({ canAccess: mocks.access[action] === true }),
  useNotify: () => mocks.notify,
  useRecordContext: () => ({ id: "dataset-1" }),
  useTranslate: () => (key: string) => key,
}));

describe("KnowledgeCompilationPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.access = { read: true, manage: true, execute: true };
    mocks.api.get.mockImplementation(async (url: string) => {
      if (url.includes("/knowledge-profiles")) {
        return { data: { data: { items: [], total: 0 } } };
      }
      if (url.includes("/compilation/runs")) {
        return {
          data: {
            data: {
              items: [{
                id: "run-1",
                state: "unknown",
                profile_id: "",
                kind: "page_index",
                manifest_hash: "",
                template_hash: "",
                pipeline_hash: "",
                document_ids: "[]",
                manifest_complete: true,
                downstream_source_snapshot_id: "",
                post_run_source_manifest_hash: "",
                source_consistency: "changed",
                detail: JSON.stringify({
                  reason: "upstream_stalled",
                  upstream_state: "running",
                  current_phase: "deleting",
                  upstream_age_seconds: 90000,
                }),
                created_at: "2026-10-10T00:00:00Z",
              }],
              total: 1,
            },
          },
        };
      }
      if (url.includes("/artifacts")) {
        return { data: { data: { items: [], total: 0 } } };
      }
      if (url.includes("/documents")) {
        return {
          data: {
            data: [
              { id: "doc-1", name: "Enabled", enabled: true, pipeline_id: "pipeline-1" },
              { id: "doc-2", name: "No Pipeline", enabled: true },
              { id: "doc-3", name: "Disabled", enabled: false, pipeline_id: "pipeline-2" },
            ],
          },
        };
      }
      throw new Error(`unexpected url ${url}`);
    });
  });

  it("renders execution controls only for authorized users and disables invalid documents", async () => {
    render(<KnowledgeCompilationPanel />);

    expect(await screen.findByRole("button", { name: "knowledgeCompilation.trigger" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "knowledgeCompilation.reconcile" })).toBeInTheDocument();
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(3);
    expect(checkboxes[0]).toBeEnabled();
    expect(checkboxes[1]).toBeDisabled();
    expect(checkboxes[2]).toBeDisabled();
    expect(screen.getByText("knowledgeCompilation.pipeline_missing")).toBeInTheDocument();
    expect(screen.getByText("knowledgeCompilation.document_disabled")).toBeInTheDocument();
    expect(screen.getByText(/knowledgeCompilation\.upstream_stalled/)).toBeInTheDocument();
    expect(screen.getByText(/25\.0h/)).toBeInTheDocument();
    expect(screen.getByText(/knowledgeCompilation\.source_consistency/)).toBeInTheDocument();
    expect(screen.getByText("knowledgeCompilation.source_changed")).toBeInTheDocument();
  });

  it("hides mutation controls from read-only users", async () => {
    mocks.access = { read: true, manage: false, execute: false };
    render(<KnowledgeCompilationPanel />);

    expect(await screen.findByText("knowledgeCompilation.runs")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "knowledgeCompilation.trigger" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "knowledgeCompilation.reconcile" })).not.toBeInTheDocument();
  });

  it("distinguishes verified artifact binding from unverified evidence", async () => {
    mocks.api.get.mockImplementation(async (url: string) => {
      if (url.includes("/knowledge-profiles") || url.includes("/compilation/runs")) {
        return { data: { data: { items: [], total: 0 } } };
      }
      if (url.includes("/documents")) {
        return { data: { data: [] } };
      }
      if (url.includes("/artifacts")) {
        return {
          data: {
            data: {
              items: [{
                id: "artifact-1", artifact_id: "page_index/manual", kind: "page_index",
                state: "ready", artifact_hash: "sha256:artifact", run_id: "run-1",
                artifact_json: JSON.stringify({ run_binding: true, content_hash_binding: true }),
              }],
              total: 1,
            },
          },
        };
      }
      throw new Error(`unexpected url ${url}`);
    });
    render(<KnowledgeCompilationPanel />);

    expect(await screen.findByText("knowledgeCompilation.artifacts_bound")).toBeInTheDocument();
    expect(screen.queryByText("knowledgeCompilation.artifacts_unverified")).not.toBeInTheDocument();
  });
});
