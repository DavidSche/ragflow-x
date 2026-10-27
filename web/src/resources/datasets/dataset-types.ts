/**
 * Shared types and utility functions for dataset management.
 */
import { api, ApiError } from "../../lib/api";

export interface DocumentRecord {
  id: string;
  name: string;
  status: string;
  enabled?: boolean;
  chunk_count?: number;
  token_count?: number;
  progress?: number;
  progress_msg?: string;
  metadata?: Record<string, unknown>;
  created_at?: number;
  updated_at?: number;
  size?: number;
  owned_by_me?: boolean;
}

export interface Envelope<T> {
  code: number;
  message: string;
  data: T;
}

export interface ChunkRecord {
  id?: string;
  content?: string;
  doc_name?: string;
  keywords?: string[];
}

export interface ParseQualityReportRecord {
  id: string;
  document_id: string;
  attempt_id: string;
  parser_policy_id: string;
  parse_mode: string;
  quality_status: string;
  gate_action: string;
  heuristic_score: number;
  reference_score?: number | null;
  effective_score: number;
  metrics?: string;
  created_at?: string;
}

export interface ParseAttemptRecord {
  id: string;
  document_id: string;
  attempt_no: number;
  parser_policy_id: string;
  parse_mode: string;
  started_at?: string;
  finished_at?: string;
  quality_status: string;
  quality_score: number;
  failure_reason?: string;
}

export interface DatasetConfig {
  id?: string;
  name?: string;
  description?: string;
  chunk_method?: string;
  embedding_model?: string;
  permission?: string;
  parser_config?: Record<string, unknown>;
}

export const RUN_TEXT: Record<string, string> = {
  "0": "待处理",
  UNSTART: "待处理",
  "1": "解析中",
  RUNNING: "解析中",
  "2": "已停止",
  CANCEL: "已停止",
  "3": "已完成",
  DONE: "已完成",
  "4": "解析失败",
  FAIL: "解析失败",
};

export function errText(err: unknown, fallback: string) {
  return err instanceof ApiError ? err.displayMessage : fallback;
}

export function formatBytes(n?: number) {
  if (!n) return "-";
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

export function formatTime(ts?: number) {
  if (!ts) return "-";
  const ms = ts > 1e12 ? ts : ts * 1000;
  return new Date(ms).toLocaleString();
}

export function documentExt(name: string) {
  const i = name.lastIndexOf(".");
  return i >= 0 ? name.slice(i + 1).toLowerCase() : "";
}

export async function loadDocuments(
  datasetId: string,
  scopeQuery = "",
): Promise<DocumentRecord[]> {
  const res = await api.get<Envelope<DocumentRecord[]>>(
    `/datasets/${datasetId}/documents${scopeQuery}`,
  );
  return res.data.data ?? [];
}
