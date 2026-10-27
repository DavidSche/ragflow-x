import { useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api, ApiError } from "../../lib/api";

interface TraceRun {
  id: string;
  trace_id: string;
  request_id: string;
  session_id?: string;
  assistant_release_id?: string;
  app_type: string;
  channel: string;
  status: string;
  route_summary_json?: string;
  retrieval_summary_json?: string;
  model_summary_json?: string;
  tool_summary_json?: string;
  governance_summary_json?: string;
  quality_summary_json?: string;
  evidence_pointers_json?: string;
}

const SUMMARY_FIELDS = [
  ["route_summary_json", "trace_route_summary"],
  ["retrieval_summary_json", "trace_retrieval_summary"],
  ["model_summary_json", "trace_model_summary"],
  ["tool_summary_json", "trace_tool_summary"],
  ["governance_summary_json", "trace_governance_summary"],
  ["quality_summary_json", "trace_quality_summary"],
  ["evidence_pointers_json", "trace_evidence_pointers"],
] as const;

function formatSummary(value?: string) {
  if (!value) return "-";
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return JSON.stringify(parsed);
    const entries = Object.entries(parsed);
    if (entries.length === 0) return "-";
    return entries.map(([key, item]) => `${key}: ${String(item)}`).join("\n");
  } catch {
    return value;
  }
}

interface OtelPointers {
  traceId: string;
  spanId: string;
  endpoint?: string;
  uiLink?: string;
}

// Deep-link contract (doc/125 §3.1, doc/104 §15): the backend stamps
// otel_trace_id / otel_span_id / optional otel_endpoint and — when the
// RGX_OTEL_TRACE_UI_URL_TEMPLATE is configured — a ready-made otel_ui_link.
// The configured link wins (Tempo/Jaeger paths differ per deployment); the
// endpoint fallback keeps the generic /trace/:id?span=:id guess.
function parseOtelPointers(evidenceJson?: string): OtelPointers | null {
  if (!evidenceJson) return null;
  try {
    const parsed = JSON.parse(evidenceJson) as Record<string, unknown>;
    const traceId = typeof parsed.otel_trace_id === "string" ? parsed.otel_trace_id : "";
    const spanId = typeof parsed.otel_span_id === "string" ? parsed.otel_span_id : "";
    if (!traceId || !spanId) return null;
    const endpoint = typeof parsed.otel_endpoint === "string" ? parsed.otel_endpoint : "";
    const rawLink = typeof parsed.otel_ui_link === "string" ? parsed.otel_ui_link : "";
    const uiLink = rawLink.startsWith("https://") || rawLink.startsWith("http://") ? rawLink : undefined;
    return { traceId, spanId, endpoint: endpoint || undefined, uiLink };
  } catch {
    return null;
  }
}

function OtelDeepLink({ pointers }: { pointers: OtelPointers }) {
  const t = useTranslate();
  const href =
    pointers.uiLink ??
    (pointers.endpoint
      ? `https://${pointers.endpoint}/trace/${encodeURIComponent(pointers.traceId)}?span=${encodeURIComponent(pointers.spanId)}`
      : null);
  return (
    <div className="rounded border p-2">
      <p className="text-xs font-medium text-muted-foreground">{t("knowledgeOps.trace_otel_ids")}</p>
      <p className="mt-1 break-all font-mono text-xs">
        {pointers.traceId}:{pointers.spanId}
      </p>
      {href ? (
        <a
          className="mt-1 inline-block text-xs font-medium text-primary underline underline-offset-4"
          href={href}
          target="_blank"
          rel="noreferrer"
        >
          {t("knowledgeOps.trace_otel_deep_link")}
        </a>
      ) : null}
    </div>
  );
}

export function TraceRunLookup() {
  const t = useTranslate();
  const notify = useNotify();
  const [query, setQuery] = useState("");
  const [trace, setTrace] = useState<TraceRun | null>(null);
  const [loading, setLoading] = useState(false);
  const otelPointers = parseOtelPointers(trace?.evidence_pointers_json);

  const lookup = async () => {
    const traceId = query.trim();
    if (!traceId) {
      notify(t("knowledgeOps.trace_lookup_required"), { type: "warning" });
      return;
    }
    setLoading(true);
    try {
      const response = await api.get<{ code: number; data: TraceRun | null }>(
        `/knowledge-ops/trace-runs/${encodeURIComponent(traceId)}`,
      );
      setTrace(response.data.data ?? null);
    } catch (err) {
      setTrace(null);
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.trace_lookup_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{t("knowledgeOps.trace_lookup_title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <form
          className="flex flex-col gap-2 sm:flex-row"
          onSubmit={(event) => {
            event.preventDefault();
            void lookup();
          }}
        >
          <input
            className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-sm"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("knowledgeOps.trace_lookup_placeholder")}
            aria-label={t("knowledgeOps.trace_lookup_placeholder")}
          />
          <Button type="submit" size="sm" disabled={loading}>{t("knowledgeOps.trace_lookup")}</Button>
        </form>

        {trace ? (
          <div className="space-y-3 rounded border p-3">
            <dl className="grid gap-2 text-sm md:grid-cols-2 xl:grid-cols-3">
              {[
                ["trace_trace_id", trace.trace_id],
                ["trace_request_id", trace.request_id],
                ["trace_session_id", trace.session_id || "-"],
                ["trace_assistant_release_id", trace.assistant_release_id || "-"],
                ["trace_app_type", trace.app_type],
                ["trace_status", trace.status],
              ].map(([label, value]) => (
                <div key={String(label)}>
                  <dt className="text-xs text-muted-foreground">{t(`knowledgeOps.${label}`)}</dt>
                  <dd className="break-all font-medium">{value}</dd>
                </div>
              ))}
            </dl>
            {otelPointers ? <OtelDeepLink pointers={otelPointers} /> : null}
            {SUMMARY_FIELDS.map(([field, label]) => (
              <div key={field} className="rounded border p-2">
                <p className="text-xs font-medium text-muted-foreground">{t(`knowledgeOps.${label}`)}</p>
                <pre className="mt-1 whitespace-pre-wrap break-all text-xs">{formatSummary(trace[field])}</pre>
              </div>
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
