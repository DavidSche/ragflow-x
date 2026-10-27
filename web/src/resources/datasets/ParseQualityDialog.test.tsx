import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ParseQualityDialog } from "./ParseQualityDialog";

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

const apiGet = vi.fn();
vi.mock("../../lib/api", () => ({
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

describe("ParseQualityDialog", () => {
  it("shows the final decision and chronological fallback attempts", async () => {
    apiGet.mockResolvedValue({
      data: {
        data: {
          items: [
            {
              id: "attempt-2",
              attempt_no: 2,
              parse_mode: "pipeline",
              quality_status: "PASS",
              quality_score: 0.94,
              started_at: "2026-01-01T00:02:00Z",
              finished_at: "2026-01-01T00:03:00Z",
            },
            {
              id: "attempt-1",
              attempt_no: 1,
              parse_mode: "builtin",
              quality_status: "FAIL",
              quality_score: 0.32,
              started_at: "2026-01-01T00:00:00Z",
              finished_at: "2026-01-01T00:01:00Z",
              failure_reason: "effective score 0.3200 is below warn threshold 0.7000",
            },
          ],
        },
      },
    });

    render(
      <ParseQualityDialog
        open
        datasetId="dataset-1"
        doc={{ id: "doc-1", name: "policy.pdf" }}
        report={{
          id: "report-1",
          document_id: "doc-1",
          attempt_id: "attempt-2",
          parser_policy_id: "policy-1",
          parse_mode: "pipeline",
          quality_status: "PASS",
          gate_action: "PUBLISH",
          heuristic_score: 0.94,
          effective_score: 0.94,
          metrics: '{"text_health":0.94}',
        }}
        scopePath={(path) => path}
        onClose={() => undefined}
      />,
    );

    await expect(screen.findAllByText(/PASS/)).resolves.toHaveLength(2);
    expect(screen.getByText(/PUBLISH/)).toBeInTheDocument();
    expect(screen.getByText(/#1 FAIL/)).toBeInTheDocument();
    expect(screen.getByText(/#2 PASS/)).toBeInTheDocument();
    expect(
      screen.getByText(/effective score 0\.3200/),
    ).toBeInTheDocument();
    expect(apiGet).toHaveBeenCalledWith(
      "/datasets/dataset-1/documents/doc-1/parse-attempts?page=1&page_size=100",
    );
  });
});
