/**
 * CitationLink – inline [ID:n] reference as a pill with a hover popover preview.
 */
import { useState } from "react";
import { useTranslate } from "ra-core";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import type { Citation } from "./workbench-types";

interface CitationLinkProps {
  index: number;
  citation?: Citation;
  onOpen: (c: Citation) => void;
}

export function CitationLink({ index, citation, onOpen }: CitationLinkProps) {
  const [open, setOpen] = useState(false);
  const t = useTranslate();
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("workbench.citation_view_ref", { index: String(index) })}
          onMouseEnter={() => setOpen(true)}
          onMouseLeave={() => setOpen(false)}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            if (citation) onOpen(citation);
          }}
          disabled={!citation}
          className="mx-0.5 inline-flex items-center rounded bg-primary/10 px-1 text-xs font-semibold text-primary hover:bg-primary/20 disabled:opacity-40"
        >
          ID:{index}
        </button>
      </PopoverTrigger>
      {citation ? (
        <PopoverContent className="z-50 w-80 p-2 text-xs" side="top" align="start">
          <div className="max-h-40 overflow-hidden whitespace-pre-wrap">{citation.content}</div>
          <div className="mt-1 truncate text-muted-foreground">{citation.name}</div>
        </PopoverContent>
      ) : null}
    </Popover>
  );
}
