/**
 * CitationWindow – modal showing a cited data block (image + text) in a
 * resizable split layout, with an original-document link when available.
 */
import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Loader2 } from "lucide-react";
import { OriginalDocumentViewer } from "@/components/documents/original-document-viewer";
import { previewFormatKind } from "@/components/documents/document-preview";
import { api } from "../../lib/api";
import { CitationImage } from "./CitationImage";
import type { Citation } from "./workbench-types";
import { isSafeSourceUri } from "./workbench-types";

interface CitationWindowProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  citation: Citation | null;
  content: string;
  loading: boolean;
  onOpenDocument: (c: Citation) => void;
  documentEnabled?: boolean;
}

export function CitationWindow({
  open,
  onOpenChange,
  citation,
  content,
  loading,
  onOpenDocument,
  documentEnabled = true,
}: CitationWindowProps) {
  const translate = useTranslate();
  const [split, setSplit] = useState(0.5);
  const containerRef = useRef<HTMLDivElement>(null);
  const draggingRef = useRef(false);
  const previewUrlRef = useRef<string | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewType, setPreviewType] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);

  const isSpreadsheet = previewFormatKind("", citation?.name) === "xlsx";
  const previewRequestKey = isSpreadsheet && citation?.datasetId && citation?.docId
    ? `${citation.datasetId}:${citation.docId}`
    : "";

  useEffect(() => () => {
    if (previewUrlRef.current) URL.revokeObjectURL(previewUrlRef.current);
  }, []);

  useEffect(() => {
    if (!previewRequestKey) {
      if (previewUrlRef.current) URL.revokeObjectURL(previewUrlRef.current);
      previewUrlRef.current = null;
      setPreviewUrl(null);
      setPreviewType("");
      setPreviewLoading(false);
      return;
    }

    let active = true;
    setPreviewLoading(true);
    (async () => {
      try {
        const response = await api.get(
          `/chat/document/preview?dataset=${encodeURIComponent(citation!.datasetId!)}&doc=${encodeURIComponent(citation!.docId!)}`,
          { responseType: "blob" },
        );
        if (!active) return;
        const blob = response.data as Blob;
        const url = URL.createObjectURL(blob);
        if (previewUrlRef.current) URL.revokeObjectURL(previewUrlRef.current);
        previewUrlRef.current = url;
        setPreviewUrl(url);
        setPreviewType(String(response.headers["content-type"] ?? ""));
      } catch {
        if (!active) return;
        if (previewUrlRef.current) URL.revokeObjectURL(previewUrlRef.current);
        previewUrlRef.current = null;
        setPreviewUrl(null);
        setPreviewType("");
      } finally {
        if (active) setPreviewLoading(false);
      }
    })();

    return () => {
      active = false;
    };
  }, [previewRequestKey]);

  const start = (e: ReactPointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    draggingRef.current = true;
  };
  const move = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current) return;
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect || rect.width === 0) return;
    const pct = (e.clientX - rect.left) / rect.width;
    setSplit(Math.min(0.85, Math.max(0.15, pct)));
  };
  const end = () => {
    draggingRef.current = false;
  };

  const hasImage = !!citation?.imageId;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            {translate("workbench.doc_chunks_title")} · {citation?.name}
          </DialogTitle>
        </DialogHeader>

        <div
          ref={containerRef}
          className="flex h-[60vh] min-h-[24rem] flex-col overflow-hidden lg:h-[65vh] lg:flex-row"
        >
          <div
            className="min-h-48 shrink-0 lg:min-h-0 lg:h-full"
            style={{ width: hasImage ? `${split * 100}%` : "100%" }}
          >
            {hasImage ? (
              <CitationImage
                imageId={citation.imageId!}
                datasetId={citation.datasetId}
                docId={citation.docId}
                chunkId={citation.chunkId}
                alt={citation.name}
                className="h-full w-full object-contain"
              />
            ) : isSpreadsheet ? (
              <OriginalDocumentViewer
                blobUrl={previewUrl ?? undefined}
                contentType={previewType}
                filename={citation?.name}
                loading={previewLoading}
              />
            ) : (
              <div className="flex h-full w-full items-center justify-center rounded border bg-muted/40 text-xs text-muted-foreground">
                {translate("workbench.no_content")}
              </div>
            )}
          </div>

          {hasImage ? (
            <div
              onPointerDown={start}
              onPointerMove={move}
              onPointerUp={end}
              onPointerCancel={end}
              role="separator"
              aria-orientation="vertical"
              className="hidden w-1.5 shrink-0 cursor-col-resize touch-none bg-muted/70 hover:bg-primary/50 lg:block"
              title={translate("workbench.split_divider")}
            />
          ) : null}

          <div className="min-h-24 min-w-0 flex-1 overflow-y-auto whitespace-pre-wrap rounded border bg-muted/40 p-3 text-sm">
            {loading ? (
              <span className="flex items-center gap-1 text-muted-foreground">
                <Loader2 className="size-3 animate-spin" /> {translate("workbench.loading")}
              </span>
            ) : (
              content || translate("workbench.citation_no_content")
            )}
          </div>
        </div>

        {citation && citation.datasetId && citation.docId ? (
          <div className="mt-2 flex justify-end">
            <Button size="sm" variant="outline" disabled={!documentEnabled} onClick={() => onOpenDocument(citation)}>
              {translate("workbench.citation_view_doc")}
            </Button>
          </div>
        ) : null}
        {citation?.sourceUri && isSafeSourceUri(citation.sourceUri) ? (
          <div className="mt-2 flex justify-end">
            <a href={citation.sourceUri} target="_blank" rel="noreferrer" className="text-sm text-primary underline">
              {translate("workbench.citation_source")}
            </a>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
