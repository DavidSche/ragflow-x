/**
 * InlineCitations – citation pills below an assistant answer, deduplicated by
 * source document. The count reflects how many distinct [ID:n] references the
 * answer actually made to that document (not the raw retrieved chunk count).
 */
import { useState } from "react";
import { useTranslate } from "ra-core";
import { BookOpen, ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { extractCitedIds } from "./workbench-types";
import type { Citation } from "./workbench-types";

interface InlineCitationsProps {
  content?: string;
  citations: Citation[];
  onOpen: (c: Citation) => void;
  onOpenDocument: (c: Citation) => void;
}

interface CitationGroup {
  name: string;
  datasetId?: string;
  docId?: string;
  count: number;
  sample: Citation;
}

function keyOf(c: Citation) {
  return `${c.datasetId ?? ""}|${c.docId ?? ""}|${c.name}`;
}

function groupCitedDocuments(content: string | undefined, citations: Citation[]): CitationGroup[] {
  const map = new Map<string, CitationGroup>();
  const ids = content ? extractCitedIds(content) : [];
  if (ids.length === 0) {
    for (const c of citations) {
      const k = keyOf(c);
      const g = map.get(k);
      if (g) g.count += 1;
      else map.set(k, { name: c.name, datasetId: c.datasetId, docId: c.docId, count: 1, sample: c });
    }
    return [...map.values()];
  }
  for (const id of ids) {
    const c = citations?.[id];
    if (!c) continue;
    const k = keyOf(c);
    const g = map.get(k);
    if (g) {
      g.count += 1;
    } else {
      map.set(k, { name: c.name, datasetId: c.datasetId, docId: c.docId, count: 1, sample: c });
    }
  }
  return [...map.values()];
}

export function InlineCitations({ content, citations, onOpen, onOpenDocument }: InlineCitationsProps) {
  const t = useTranslate();
  const groups = groupCitedDocuments(content, citations);
  const [expanded, setExpanded] = useState(false);
  if (groups.length === 0) {
    return <div className="mt-1 text-left" />;
  }
  return (
    <div className="mt-1 text-left">
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-7 rounded-full px-3 text-xs"
        aria-expanded={expanded}
        onClick={() => setExpanded((previous) => !previous)}
      >
        {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        {t("workbench.citation_source_count").replace("{count}", String(groups.length))}
      </Button>
      {expanded ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {groups.map((group, index) => (
            <div key={`${group.name}-${index}`} className="flex items-center gap-1 rounded border bg-background/60 px-2 py-1 text-xs">
              <button
                type="button"
                className="max-w-[16rem] truncate"
                onClick={() => onOpen(group.sample)}
                title={group.sample.content}
                aria-label={`${t("workbench.citation_source")}：${group.name}`}
              >
                {group.name}
              </button>
              {group.count > 1 ? <span className="text-muted-foreground">×{group.count}</span> : null}
              {group.sample.datasetId && group.sample.docId ? (
                <Button
                  size="icon"
                  variant="ghost"
                  className="h-5 w-5"
                  onClick={() => onOpenDocument(group.sample)}
                  title={t("workbench.citation_view_doc")}
                  aria-label={`${t("workbench.citation_view_doc")}：${group.name}`}
                >
                  <BookOpen className="size-3.5" />
                </Button>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
