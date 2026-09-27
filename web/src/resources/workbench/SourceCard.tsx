/**
 * SourceCard – citation source card shown in the sidebar rail.
 */
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { BookOpen, Copy, Eye } from "lucide-react";
import { CitationImage } from "./CitationImage";
import type { Citation } from "./workbench-types";

interface SourceCardProps {
  index: number;
  citation: Citation;
  onCopy: (t: string) => void;
  onOpen: (c: Citation) => void;
  onOpenDocument: (c: Citation) => void;
}

export function SourceCard({ index, citation, onCopy, onOpen, onOpenDocument }: SourceCardProps) {
  const t = useTranslate();
  return (
    <div className="rounded border bg-background/60 px-2 py-1">
      <div className="flex items-center justify-between gap-1">
        <span className="truncate text-xs font-medium">
          [{index}] {citation.name}
        </span>
        <div className="flex gap-1">
          <Button
            size="icon"
            variant="ghost"
            className="h-5 w-5"
            onClick={() => void onOpen(citation)}
            title={t("workbench.citation_view")}
            aria-label={t("workbench.citation_view")}
          >
            <Eye className="size-3" />
          </Button>
          {citation.datasetId && citation.docId ? (
            <Button size="icon" variant="ghost" className="h-5 w-5" onClick={() => onOpenDocument(citation)} title={t("workbench.citation_view_doc")} aria-label={t("workbench.citation_view_doc")}>
              <BookOpen className="size-3" />
            </Button>
          ) : null}
          <Button
            size="icon"
            variant="ghost"
            className="h-5 w-5"
            onClick={() => void onCopy(`${citation.name}\n${citation.content}`)}
            title={t("workbench.citation_copy_source")}
            aria-label={t("workbench.citation_copy_source")}
          >
            <Copy className="size-3" />
          </Button>
        </div>
      </div>
      {citation.imageId ? (
        <CitationImage
          imageId={citation.imageId}
          datasetId={citation.datasetId}
          docId={citation.docId}
          chunkId={citation.chunkId}
          alt={citation.name}
          className="mt-1 max-h-40 w-full rounded object-contain bg-muted/40"
        />
      ) : null}

      <div className="mt-1 max-h-32 overflow-y-auto whitespace-pre-wrap text-xs text-muted-foreground">
        {citation.content}
      </div>
    </div>
  );
}
