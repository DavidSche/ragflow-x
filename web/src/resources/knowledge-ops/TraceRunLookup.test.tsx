import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TraceRunLookup } from "./TraceRunLookup";

const apiGet = vi.fn();

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

describe("<TraceRunLookup />", () => {
  beforeEach(() => {
    apiGet.mockReset();
  });

  it("opens the governance chain by trace id", async () => {
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      id: "run-1", trace_id: "trace-1", request_id: "request-1", session_id: "session-1",
      assistant_release_id: "release-1", app_type: "chat", channel: "web", status: "completed",
      route_summary_json: JSON.stringify({selection: "finance-assistant"}),
      retrieval_summary_json: "", model_summary_json: "", tool_summary_json: "",
      governance_summary_json: "", quality_summary_json: "", evidence_pointers_json: "",
    } } });
    render(<TraceRunLookup />);
    fireEvent.change(screen.getByLabelText("knowledgeOps.trace_lookup_placeholder"), { target: { value: "trace-1" } });
    fireEvent.click(screen.getByRole("button", { name: "knowledgeOps.trace_lookup" }));

    await waitFor(() => expect(apiGet).toHaveBeenCalledWith("/knowledge-ops/trace-runs/trace-1"));
    expect(screen.getByText("release-1")).toBeInTheDocument();
    expect(screen.getByText(/finance-assistant/)).toBeInTheDocument();
  });

  it("renders the configured otel_ui_link over the endpoint fallback", async () => {
    const evidence = JSON.stringify({
      otel_trace_id: "0af7651916cd43dd8448eb211c80319c",
      otel_span_id: "b7ad6b7169203331",
      otel_endpoint: "collector:4318",
      otel_ui_link: "https://grafana.example.com/trace/0af7651916cd43dd8448eb211c80319c?span=b7ad6b7169203331",
    });
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      id: "run-otel", trace_id: "request-otel", request_id: "request-otel", app_type: "chat",
      channel: "web", status: "completed", evidence_pointers_json: evidence,
    } } });
    render(<TraceRunLookup />);
    fireEvent.change(screen.getByLabelText("knowledgeOps.trace_lookup_placeholder"), { target: { value: "request-otel" } });
    fireEvent.click(screen.getByRole("button", { name: "knowledgeOps.trace_lookup" }));

    const link = await screen.findByRole("link", { name: "knowledgeOps.trace_otel_deep_link" });
    expect(link).toHaveAttribute("href", "https://grafana.example.com/trace/0af7651916cd43dd8448eb211c80319c?span=b7ad6b7169203331");
  });

  it("falls back to the endpoint guess when no ui link is configured", async () => {
    const evidence = JSON.stringify({
      otel_trace_id: "0af7651916cd43dd8448eb211c80319c",
      otel_span_id: "b7ad6b7169203331",
      otel_endpoint: "tempo.internal:4318",
    });
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      id: "run-otel-2", trace_id: "request-otel-2", request_id: "request-otel-2", app_type: "chat",
      channel: "web", status: "completed", evidence_pointers_json: evidence,
    } } });
    render(<TraceRunLookup />);
    fireEvent.change(screen.getByLabelText("knowledgeOps.trace_lookup_placeholder"), { target: { value: "request-otel-2" } });
    fireEvent.click(screen.getByRole("button", { name: "knowledgeOps.trace_lookup" }));

    const link = await screen.findByRole("link", { name: "knowledgeOps.trace_otel_deep_link" });
    expect(link).toHaveAttribute(
      "href",
      "https://tempo.internal:4318/trace/0af7651916cd43dd8448eb211c80319c?span=b7ad6b7169203331",
    );
  });

  it("shows ids as text when neither ui link nor endpoint exists", async () => {
    const evidence = JSON.stringify({
      otel_trace_id: "0af7651916cd43dd8448eb211c80319c",
      otel_span_id: "b7ad6b7169203331",
    });
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      id: "run-otel-3", trace_id: "request-otel-3", request_id: "request-otel-3", app_type: "chat",
      channel: "web", status: "completed", evidence_pointers_json: evidence,
    } } });
    render(<TraceRunLookup />);
    fireEvent.change(screen.getByLabelText("knowledgeOps.trace_lookup_placeholder"), { target: { value: "request-otel-3" } });
    fireEvent.click(screen.getByRole("button", { name: "knowledgeOps.trace_lookup" }));

    await screen.findByText(/0af7651916cd43dd8448eb211c80319c:b7ad6b7169203331/);
    expect(screen.queryByRole("link", { name: "knowledgeOps.trace_otel_deep_link" })).not.toBeInTheDocument();
  });
});
