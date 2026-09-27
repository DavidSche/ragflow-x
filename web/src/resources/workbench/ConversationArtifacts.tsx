import { useState } from "react";
import { Download } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { type ConversationArtifact, type ConversationChartArtifact } from "./workbench-types";

interface Props {
  artifacts?: ConversationArtifact[];
}

const artifactId = (artifact: ConversationArtifact, index: number) =>
  `conversation-artifact-${artifact.type}-${index}`;

function normalizeTable(data: unknown): { headers: string[]; rows: string[][] } | null {
  if (Array.isArray(data) && data.every((row) => Array.isArray(row))) {
    const raw = data as unknown[][];
    if (!raw.length) return null;
    return {
      headers: raw[0].map((cell) => String(cell ?? "")),
      rows: raw.slice(1).map((row) => row.map((cell) => String(cell ?? ""))),
    };
  }
  if (Array.isArray(data) && data.length && data.every((row) => row && typeof row === "object")) {
    const records = data as Record<string, unknown>[];
    const headers = [...new Set(records.flatMap((record) => Object.keys(record)))];
    return { headers, rows: records.map((record) => headers.map((key) => String(record[key] ?? ""))) };
  }
  if (data && typeof data === "object" && Array.isArray((data as { columns?: unknown }).columns)) {
    const value = data as { columns?: unknown; rows?: unknown };
    if (!Array.isArray(value.columns) || !Array.isArray(value.rows)) return null;
    return {
      headers: value.columns.map((cell) => String(cell ?? "")),
      rows: value.rows.map((row) =>
        Array.isArray(row) ? row.map((cell) => String(cell ?? "")) : [String(row ?? "")],
      ),
    };
  }
  return null;
}

function csvValue(value: string): string {
  return `"${value.replace(/"/g, '""')}"`;
}

function downloadCsv(table: { headers: string[]; rows: string[][] }, filename: string): void {
  const lines = [table.headers.map(csvValue).join(","), ...table.rows.map((row) => row.map(csvValue).join(","))];
  const url = URL.createObjectURL(new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${filename}.csv`;
  anchor.click();
  URL.revokeObjectURL(url);
}

function svgDataUrl(svg: string): string {
  return ["data", "image/svg+xml;charset=utf-8," + encodeURIComponent(svg)].join(":");
}

function downloadSvgAsPng(svg: SVGSVGElement | null, filename: string, onError?: (message: string) => void): void {
  if (!svg) {
    onError?.("找不到可导出的图表");
    return;
  }
  const serialized = new XMLSerializer().serializeToString(svg);
  const image = new Image();
  image.onload = () => {
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(svg.viewBox.baseVal.width || svg.clientWidth || 480, 1);
    canvas.height = Math.max(svg.viewBox.baseVal.height || svg.clientHeight || 280, 1);
    const context = canvas.getContext("2d");
    if (!context) {
      onError?.("当前浏览器不支持 PNG 导出，请改用 SVG");
      return;
    }
    const anchor = document.createElement("a");
    context.fillStyle = "#ffffff";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    const downloadCanvas = () => {
      try {
        const dataUrl = canvas.toDataURL("image/png");
        if (!dataUrl.startsWith("[image omitted]")) throw new Error("empty data url");
        anchor.href = dataUrl;
        anchor.download = `${filename}.png`;
        anchor.click();
      } catch {
        onError?.("当前浏览器不支持 PNG 导出，请改用 SVG");
      }
    };
    if (typeof canvas.toBlob !== "function") {
      downloadCanvas();
      return;
    }
    canvas.toBlob((blob) => {
      if (!blob) {
        downloadCanvas();
        return;
      }
      const url = URL.createObjectURL(blob);
      anchor.href = url;
      anchor.download = `${filename}.png`;
      anchor.click();
      URL.revokeObjectURL(url);
    }, "image/png");
  };
  image.onerror = () => onError?.("图表导出失败，请重试");
  image.src = svgDataUrl(serialized);
}

function svgNode(parent: Element | null): SVGSVGElement | null {
  return parent?.querySelector<SVGSVGElement>("svg") ?? null;
}

type ChartSpec = {
  type?: "bar" | "line" | "area";
  labels?: unknown[];
  datasets?: { label?: string; data?: unknown[]; color?: string }[];
  unit?: string;
};

function parseChart(data: unknown): ChartSpec | null {
  if (!data || typeof data !== "object") return null;
  const spec = data as ChartSpec;
  if (!Array.isArray(spec.labels) || !Array.isArray(spec.datasets) || !spec.datasets.length) return null;
  return spec;
}

function ChartArtifact({ artifact, filename }: { artifact: ConversationChartArtifact; filename: string }) {
  const [exportError, setExportError] = useState("");
  const spec = parseChart(artifact.data);
  if (!spec) return null;
  const chartType = spec.type === "line" || spec.type === "area" ? spec.type : "bar";
  const labels = (spec.labels ?? []).map((item) => String(item ?? ""));
  const datasets = spec.datasets ?? [];
  const colors = ["#2563eb", "#16a34a", "#f97316", "#7c3aed", "#0891b2", "#dc2626"];
  const width = 640;
  const height = 300;
  const left = 48;
  const bottom = 36;
  const chartWidth = width - left - 12;
  const chartHeight = height - bottom - 16;
  const values = datasets.flatMap((dataset) => (dataset.data ?? []).map(Number)).filter(Number.isFinite);
  const rawMax = values.length ? Math.max(...values) : 1;
  const rawMin = values.length ? Math.min(...values) : 0;
  const maxValue = rawMax >= 0 ? Math.max(rawMax, 1) : rawMax;
  const minValue = rawMin < 0 ? rawMin : 0;
  const range = maxValue - minValue || 1;
  const pointY = (value: number) => height - bottom - ((value - minValue) / range) * chartHeight;
  const ticks = Array.from({ length: 5 }, (_, index) => minValue + (range / 4) * index);
  const formatTick = (value: number) => (Math.abs(value) >= 10 ? String(Math.round(value)) : value.toFixed(1));
  const linePoints = (data: unknown[]) => data
    .map((value, index) => ({ value: Number(value), index }))
    .filter((point) => Number.isFinite(point.value))
    .map((point) => {
      const slot = chartWidth / Math.max(1, labels.length);
      return { x: left + slot * point.index + slot / 2, y: pointY(point.value) };
    });

  return (
    <figure className="rounded-md border bg-background p-3">
      <figcaption className="mb-2 flex items-center justify-between gap-2 text-sm font-medium">
        <span>{artifact.title || "图表"}{spec.unit ? `（${spec.unit}）` : ""}</span>
        <Button
          size="icon"
          variant="ghost"
          className="size-6"
          aria-label="导出图表 PNG"
          onClick={(event) => downloadSvgAsPng(svgNode(event.currentTarget.closest("figure")), filename, setExportError)}
        >
          <Download className="size-3.5" />
        </Button>
      </figcaption>
      {exportError ? <p className="mb-2 text-xs text-destructive">{exportError}</p> : null}
      <svg viewBox={`0 0 ${width} ${height}`} className="h-auto w-full" role="img" aria-label={artifact.title || "图表"}>
        <line x1={left} y1={16} x2={left} y2={height - bottom} stroke="#e5e7eb" />
        <line x1={left} y1={height - bottom} x2={width - 12} y2={height - bottom} stroke="#e5e7eb" />
        {ticks.map((tick) => (
          <g key={tick}>
            <line x1={left} y1={pointY(tick)} x2={width - 12} y2={pointY(tick)} stroke="#f3f4f6" />
            <text x={left - 8} y={pointY(tick) + 3} textAnchor="end" fontSize="10" fill="#6b7280">
              {formatTick(tick)}
            </text>
          </g>
        ))}
        {datasets.map((dataset, datasetIndex) => {
          const color = dataset.color || colors[datasetIndex % colors.length];
          const points = linePoints(dataset.data ?? []);
          if (chartType !== "bar" && points.length > 1) {
            const line = points.map((point) => `${point.x},${point.y}`).join(" ");
            return chartType === "area" ? (
              <polygon
                key={datasetIndex}
                points={`${left},${height - bottom} ${line} ${points[points.length - 1].x},${height - bottom}`}
                fill={`${color}22`}
                stroke={color}
                strokeWidth="2"
              />
            ) : (
              <polyline
                key={datasetIndex}
                points={line}
                fill="none"
                stroke={color}
                strokeWidth="2"
                strokeLinejoin="round"
                strokeLinecap="round"
              />
            );
          }
          return (dataset.data ?? []).map((value, valueIndex) => {
            const numeric = Number(value);
            if (!Number.isFinite(numeric)) return null;
            const count = Math.max(1, labels.length);
            const groupSlot = chartWidth / count;
            const seriesSlot = groupSlot / Math.max(1, datasets.length);
            const seriesWidth = Math.min(seriesSlot * 0.7, 36);
            const pointX = left + groupSlot * valueIndex + seriesSlot * (datasetIndex + 0.5);
            return chartType === "bar" ? (
              <rect
                key={`${datasetIndex}-${valueIndex}`}
                x={pointX - seriesWidth / 2}
                y={pointY(numeric)}
                width={seriesWidth}
                height={Math.max(height - bottom - pointY(numeric), 1)}
                fill={color}
                rx="3"
              />
            ) : (
              <circle key={`${datasetIndex}-${valueIndex}`} cx={pointX} cy={pointY(numeric)} r="4" fill={color} />
            );
          });
        })}
        {labels.map((label, index) => {
          const slot = chartWidth / Math.max(1, labels.length);
          return (
            <text key={index} x={left + slot * index + slot / 2} y={height - 14} textAnchor="middle" fontSize="10" fill="#6b7280">
              {label}
            </text>
          );
        })}
      </svg>
      <div className="mt-2 flex flex-wrap justify-end gap-2 text-xs text-muted-foreground">
        {datasets.map((dataset, index) => (
          <span key={index} className="inline-flex items-center gap-1">
            <span className="size-2 rounded-full" style={{ background: dataset.color || colors[index % colors.length] }} />
            {dataset.label || `数据集 ${index + 1}`}
          </span>
        ))}
      </div>
    </figure>
  );
}

export function ConversationArtifacts({ artifacts }: Props) {
  if (!artifacts?.length) return null;
  return (
    <div className="mt-2 space-y-2">
      {artifacts.map((artifact, index) => {
        const filename = (artifact.title || artifactId(artifact, index)).replace(/[\\/:*?"<>|]/g, "-");
        return (
          <div key={index}>
            {artifact.type === "table" ? (() => {
              const table = normalizeTable(artifact.data);
              if (!table) return null;
              return (
                <figure className="rounded-md border">
                  <figcaption className="flex items-center justify-between gap-2 border-b bg-muted/40 p-2 text-sm font-medium">
                    <span>{artifact.title || "表格"}</span>
                    <Button size="icon" variant="ghost" className="size-6" aria-label="导出表格 CSV" onClick={() => downloadCsv(table, filename)}>
                      <Download className="size-3.5" />
                    </Button>
                  </figcaption>
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead className="bg-muted/20">
                        <tr>{table.headers.map((header, headerIndex) => <th key={headerIndex} className="p-2 text-left font-medium">{header}</th>)}</tr>
                      </thead>
                      <tbody>
                        {table.rows.map((row, rowIndex) => (
                          <tr key={rowIndex} className="border-t">
                            {row.map((cell, cellIndex) => <td key={cellIndex} className="p-2 align-top">{cell}</td>)}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </figure>
              );
            })() : null}
            {artifact.type === "chart" ? <ChartArtifact artifact={artifact} filename={filename} /> : null}
            {artifact.type === "card" ? (
              <div className="rounded-md border p-3">
                <Badge variant="secondary">{artifact.title || "卡片"}</Badge>
                <pre className="mt-2 whitespace-pre-wrap font-sans text-sm">{artifact.content}</pre>
              </div>
            ) : null}
            {artifact.type === "file" ? (
              <a href={artifact.url || "#"} download={artifact.title || filename} className="inline-flex items-center gap-2 rounded border px-3 py-2 text-sm hover:bg-muted/50">
                <Download className="size-4" />
                {artifact.title || filename}
              </a>
            ) : null}
            {artifact.type === "link" && artifact.url && /^(https?:|\/|#)/i.test(artifact.url) ? (
              <a href={artifact.url} target="_blank" rel="noreferrer" className="inline-flex max-w-full items-center gap-2 rounded border px-3 py-2 text-sm text-primary underline">
                {artifact.title || artifact.url}
              </a>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
