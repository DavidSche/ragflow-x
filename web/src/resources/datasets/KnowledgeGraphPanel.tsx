/**
 * KnowledgeGraphPanel – read-only knowledge graph view for one dataset (C6).
 *
 * Renders the engine-produced graph (GET /datasets/{id}/graph) as a lightweight
 * SVG force-free layout: nodes keep their engine-provided positions when
 * present, otherwise they fall back to a deterministic circle layout. No
 * editing, no mutations — the graph is compilation output.
 */
import { useCallback, useEffect, useState } from "react";
import { useGetIdentity, useNotify, useRecordContext, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Network, RefreshCw } from "lucide-react";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface GraphNode {
  id: string;
  labels?: string[];
  type?: string;
  x?: number;
  y?: number;
}

interface GraphEdge {
  source: string;
  target: string;
  weight?: number;
  description?: string;
}

interface DatasetGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

const VIEW_WIDTH = 560;
const VIEW_HEIGHT = 320;

export const KnowledgeGraphPanel = () => {
  const record = useRecordContext<{ id?: string; tenant_id?: string }>();
  const datasetId = record?.id;
  const t = useTranslate();
  const notify = useNotify();
  const { data: identity } = useGetIdentity();
  const identityTenantID = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const recordTenantID = record?.tenant_id;
  const scopeQuery =
    identityTenantID && recordTenantID && identityTenantID !== recordTenantID
      ? `?scope=specific&tenant_id=${encodeURIComponent(recordTenantID)}`
      : "";

  const [graph, setGraph] = useState<DatasetGraph | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    if (!datasetId) return;
    setLoading(true);
    try {
      const suffix = scopeQuery ? scopeQuery.replace("?", "&") : "";
      const data = await unwrap(
        api.get<ApiEnvelope<DatasetGraph>>(
          `/datasets/${encodeURIComponent(datasetId)}/graph${suffix}`,
        ),
      );
      setGraph({ nodes: data.nodes ?? [], edges: data.edges ?? [] });
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("datasets.graph_load_failed"), {
        type: "error",
      });
    } finally {
      setLoading(false);
    }
  }, [datasetId, scopeQuery, notify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const nodes = graph?.nodes ?? [];
  const edges = graph?.edges ?? [];

  // Deterministic fallback layout: place nodes without engine positions on a
  // circle so every render is stable for the same graph payload.
  const positions = new Map<string, { x: number; y: number }>();
  const missing = nodes.filter((node) => typeof node.x !== "number" || typeof node.y !== "number");
  missing.forEach((node, index) => {
    const angle = (2 * Math.PI * index) / Math.max(missing.length, 1);
    positions.set(node.id, {
      x: VIEW_WIDTH / 2 + (VIEW_WIDTH / 2 - 48) * Math.cos(angle),
      y: VIEW_HEIGHT / 2 + (VIEW_HEIGHT / 2 - 40) * Math.sin(angle),
    });
  });
  nodes.forEach((node) => {
    if (typeof node.x === "number" && typeof node.y === "number") {
      positions.set(node.id, { x: node.x * VIEW_WIDTH, y: node.y * VIEW_HEIGHT });
    }
  });

  return (
    <Card aria-label={t("datasets.graph_title")}>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle className="flex items-center gap-2 text-base">
          <Network className="size-4" aria-hidden />
          {t("datasets.graph_title")}
        </CardTitle>
        <Button variant="ghost" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={loading ? "size-4 animate-spin" : "size-4"} aria-hidden />
          {t("datasets.graph_refresh")}
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        {nodes.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("datasets.graph_empty")}</p>
        ) : (
          <>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Badge variant="secondary">{t("datasets.graph_nodes", { count: nodes.length })}</Badge>
              <Badge variant="secondary">{t("datasets.graph_edges", { count: edges.length })}</Badge>
            </div>
            <svg
              role="img"
              aria-label={t("datasets.graph_title")}
              viewBox={`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`}
              className="w-full rounded-md border border-border bg-background"
              data-testid="knowledge-graph-svg"
            >
              {edges.map((edge, index) => {
                const from = positions.get(edge.source);
                const to = positions.get(edge.target);
                if (!from || !to) return null;
                return (
                  <line
                    key={`${edge.source}->${edge.target}:${index}`}
                    x1={from.x}
                    y1={from.y}
                    x2={to.x}
                    y2={to.y}
                    stroke="currentColor"
                    strokeWidth={Math.max(edge.weight ?? 1, 0.5)}
                    className="text-muted-foreground/50"
                  >
                    {edge.description ? <title>{edge.description}</title> : null}
                  </line>
                );
              })}
              {nodes.map((node) => {
                const pos = positions.get(node.id);
                if (!pos) return null;
                const label = node.labels?.[0] ?? node.id;
                return (
                  <g key={node.id} transform={`translate(${pos.x}, ${pos.y})`}>
                    <circle r={6} className="fill-primary" data-testid="graph-node">
                      <title>{label}</title>
                    </circle>
                    <text
                      x={8}
                      y={4}
                      className="fill-foreground text-[10px]"
                      aria-hidden
                    >
                      {label.length > 24 ? `${label.slice(0, 23)}…` : label}
                    </text>
                  </g>
                );
              })}
            </svg>
          </>
        )}
      </CardContent>
    </Card>
  );
};
