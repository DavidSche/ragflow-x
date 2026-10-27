import { useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Loader2 } from "lucide-react";
import { OriginalDocumentViewer } from "@/components/documents/original-document-viewer";
import { isTextPreview } from "@/components/documents/document-preview";
import { CitationWindow } from "../workbench/CitationWindow";
import type { Citation } from "../workbench/workbench-types";
import { api } from "../../lib/api";

interface CitationViewerProps {
  citation: Citation | null;
  onCitationChange: (citation: Citation | null) => void;
  documentEnabled: boolean;
}

export function ConversationCitationViewer({ citation, onCitationChange, documentEnabled }: CitationViewerProps) {
  const t = useTranslate();
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(false);
  const [document, setDocument] = useState<Citation | null>(null);
  const [view, setView] = useState<"chunks" | "original">("chunks");
  const [chunks, setChunks] = useState<Array<{ id: string; content: string }>>([]);
  const [chunksLoading, setChunksLoading] = useState(false);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewType, setPreviewType] = useState("");
  const [previewText, setPreviewText] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);

  useEffect(() => () => {
    if (previewUrl) URL.revokeObjectURL(previewUrl);
  }, [previewUrl]);

  useEffect(() => {
    if (!citation) return;
    setContent(citation.content || t("workbench.citation_no_content"));
    setLoading(false);
    if (!(citation.datasetId && citation.docId && citation.chunkId)) return;

    let active = true;
    setContent(citation.content || "");
    setLoading(true);
    (async () => {
      try {
        const query = `dataset=${encodeURIComponent(citation.datasetId!)}&doc=${encodeURIComponent(citation.docId!)}&chunk=${encodeURIComponent(citation.chunkId!)}`;
        const response = await api.get<{ code: number; data?: { content?: string; content_with_weight?: string } }>(`/chat/reference?${query}`);
        const fetched = response.data.data?.content ?? response.data.data?.content_with_weight ?? "";
        if (active && fetched) setContent(fetched);
      } catch {
        if (active && !citation.content) setContent(t("workbench.citation_load_fail"));
      } finally {
        if (active) setLoading(false);
      }
    })();

    return () => {
      active = false;
    };
  }, [citation, t]);

  const openDocument = async (nextCitation: Citation) => {
    if (!documentEnabled || !nextCitation.datasetId || !nextCitation.docId) return;
    setDocument(nextCitation);
    setView("chunks");
    setChunks([]);
    setChunksLoading(true);
    setPreviewLoading(true);
    try {
      const query = `dataset=${encodeURIComponent(nextCitation.datasetId)}&doc=${encodeURIComponent(nextCitation.docId)}&page=1&page_size=100`;
      const response = await api.get<{ code: number; data?: { items?: Array<{ id?: string; content?: string }> } }>(`/chat/document/chunks?${query}`);
      setChunks((response.data.data?.items ?? []).map((item) => ({ id: String(item?.id ?? ""), content: String(item?.content ?? "") })));
    } catch {
      setChunks([{ id: "", content: t("workbench.doc_load_fail") }]);
    }
    setChunksLoading(false);

    try {
      const response = await api.get(`/chat/document/preview?doc=${encodeURIComponent(nextCitation.docId)}`, { responseType: "blob" });
      const blob = response.data as Blob;
      if (previewUrl) URL.revokeObjectURL(previewUrl);
      const contentType = String(response.headers["content-type"] ?? "");
      const url = URL.createObjectURL(blob);
      setPreviewUrl(url);
      setPreviewType(contentType);
      setPreviewText(isTextPreview(contentType, nextCitation.name) ? await blob.text() : "");
    } catch {
      setPreviewUrl(null);
      setPreviewType("");
      setPreviewText("");
    }
    setPreviewLoading(false);
  };

  return (
    <>
      <CitationWindow
        open={!!citation}
        onOpenChange={(open) => !open && onCitationChange(null)}
        citation={citation}
        content={content}
        loading={loading}
        documentEnabled={documentEnabled}
        onOpenDocument={(nextCitation) => void openDocument(nextCitation)}
      />
      <Dialog
        open={!!document}
        onOpenChange={(open) => {
          if (open) return;
          if (previewUrl) URL.revokeObjectURL(previewUrl);
          setPreviewUrl(null);
          setPreviewText("");
          setPreviewType("");
          setDocument(null);
        }}
      >
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t("workbench.doc_view_title")} · {document?.name}</DialogTitle>
          </DialogHeader>
          <div className="mb-2 flex gap-1">
            <Button size="sm" variant={view === "chunks" ? "default" : "outline"} onClick={() => setView("chunks")}>{t("workbench.doc_chunks")}</Button>
            <Button size="sm" variant={view === "original" ? "default" : "outline"} onClick={() => setView("original")}>{t("workbench.doc_original")}</Button>
          </div>
          {view === "chunks" ? (
            <div className="max-h-[64vh] space-y-2 overflow-y-auto text-sm">
              {chunksLoading ? (
                <span className="flex items-center gap-1 text-muted-foreground"><Loader2 className="size-3 animate-spin" />{t("workbench.loading")}</span>
              ) : chunks.length === 0 ? (
                <div className="text-muted-foreground">{t("workbench.no_content")}</div>
              ) : chunks.map((chunk, index) => (
                <div key={chunk.id || index} className="rounded border bg-muted/40 p-2"><pre className="whitespace-pre-wrap">{chunk.content}</pre></div>
              ))}
            </div>
          ) : (
            <div className="max-h-[64vh] overflow-auto rounded-md border bg-muted/20 p-2">
              <OriginalDocumentViewer blobUrl={previewUrl ?? undefined} contentType={previewType} text={previewText} filename={document?.name} loading={previewLoading} />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
