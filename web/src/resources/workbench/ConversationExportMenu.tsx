import { useState } from "react";
import { useTranslate } from "ra-core";
import { Download } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { downloadConversation, type ConversationExportFormat } from "./conversation-export";
import type { Message } from "./workbench-types";

interface ConversationExportMenuProps {
  messages: Message[];
  title: string;
  filenamePrefix?: string;
  disabled?: boolean;
  className?: string;
  onExport?: (format: ConversationExportFormat) => void;
}

const EXPORT_POLL_INTERVAL_MS = 2_000;
const EXPORT_POLL_TIMEOUT_MS = 60_000;

type ExportJobStatus = "QUEUED" | "RUNNING" | "SUCCEEDED" | "PARTIAL" | "FAILED" | "EXPIRED" | "CANCELLED";

interface ExportJobPayload {
  export_job?: { id?: string; status?: ExportJobStatus };
  export_artifact?: { id?: string; filename?: string };
}

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function ConversationExportMenu({
  messages,
  title,
  filenamePrefix = "conversation",
  disabled = false,
  className,
  onExport,
}: ConversationExportMenuProps) {
  const t = useTranslate();
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");
  const exportAs = (format: ConversationExportFormat) => {
    onExport?.(format);
    downloadConversation(messages, title, format, filenamePrefix);
  };
  const waitForExportJob = async (jobId: string): Promise<ExportJobPayload> => {
    const deadline = Date.now() + EXPORT_POLL_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(EXPORT_POLL_INTERVAL_MS);
      const poll = await api.get<{ code: number; data: ExportJobPayload }>(
        `/answer-snapshots/export-jobs/${encodeURIComponent(jobId)}`,
      );
      const payload = poll.data.data ?? {};
      const status = payload.export_job?.status;
      if (status === "SUCCEEDED" || status === "PARTIAL") return payload;
      if (status === "FAILED" || status === "EXPIRED" || status === "CANCELLED") {
        throw new ApiError(409, 40133, t("conversation.export_failed_retryable"));
      }
    }
    throw new ApiError(504, 40133, t("conversation.export_queued_hint"));
  };
  const downloadArtifact = (artifact: { id?: string; filename?: string }, format: "pdf" | "docx") => {
    const artifactId = artifact?.id;
    if (!artifactId) throw new ApiError(500, 40130, t("conversation.export_no_artifact"));
    return api.get(`/answer-snapshots/artifacts/${encodeURIComponent(artifactId)}/download`, {
      responseType: "blob",
    }).then((file) => {
      const url = URL.createObjectURL(file.data as Blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = artifact?.filename || `${title}.${format}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
    });
  };
  const exportConversation = async (format: "pdf" | "docx") => {
    const answer = [...messages].reverse().find((message) => message.role === "assistant" && message.turnId && !message.error);
    if (!answer?.turnId) {
      setExportError(t("conversation.export_no_answer"));
      return;
    }
    setExporting(true);
    setExportError("");
    try {
      const requestId = answer.turnId.startsWith("assistant-") ? answer.turnId.slice("assistant-".length) : answer.turnId;
      const snapshot = await api.get<{ code: number; data: { answer_snapshot?: { id?: string } } }>(
        `/answer-snapshots/by-request/${encodeURIComponent(requestId)}`,
      );
      const snapshotId = snapshot.data.data?.answer_snapshot?.id;
      if (!snapshotId) throw new ApiError(404, 40120, t("conversation.export_snapshot_missing"));
      const job = await api.post<{ code: number; data: ExportJobPayload }>(
        `/answer-snapshots/${encodeURIComponent(snapshotId)}/export`,
        { format, scope: "conversation" },
      );
      let payload = job.data.data ?? {};
      const jobId = payload.export_job?.id;
      // Async path: large exports (>256KiB) return QUEUED with no artifact.
      // Poll the job endpoint until it succeeds (doc/118 F-14).
      const status = payload.export_job?.status;
      if (!payload.export_artifact?.id && (status === "QUEUED" || status === "RUNNING")) {
        if (!jobId) throw new ApiError(500, 40130, t("conversation.export_no_artifact"));
        payload = await waitForExportJob(jobId);
      }
      await downloadArtifact(payload.export_artifact ?? {}, format);
    } catch (error) {
      setExportError(error instanceof ApiError ? error.displayMessage : t("conversation.export_failed"));
    } finally {
      setExporting(false);
    }
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="outline" disabled={disabled || !messages.length} className={className}>
          <Download className="size-4" aria-hidden="true" />
          {t("conversation.export")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => exportAs("markdown")}>Markdown</DropdownMenuItem>
        <DropdownMenuItem onClick={() => exportAs("html")}>HTML</DropdownMenuItem>
        <DropdownMenuItem onClick={() => exportAs("json")}>JSON</DropdownMenuItem>
        <DropdownMenuItem disabled={exporting} onClick={() => void exportConversation("pdf")}>
          {exporting ? t("conversation.export_running_pdf") : "PDF"}
        </DropdownMenuItem>
        <DropdownMenuItem disabled={exporting} onClick={() => void exportConversation("docx")}>
          {exporting ? t("conversation.export_running_docx") : "DOCX"}
        </DropdownMenuItem>
      </DropdownMenuContent>
      {exportError ? <p role="alert" className="px-2 py-1 text-xs text-destructive">{exportError}</p> : null}
    </DropdownMenu>
  );
}
