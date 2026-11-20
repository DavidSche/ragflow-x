/**
 * AnswerMarkdown – renders assistant answer as GFM markdown with interactive
 * citation popovers for [ID:n] tokens, proper tables and mermaid diagrams.
 */
import { lazy, Suspense } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Citation } from "./workbench-types";
import { CITATION_TOKEN_RE, resolveCitationTokens } from "./workbench-types";
import { CitationLink } from "./CitationLink";

const MermaidBlock = lazy(() =>
  import("./MermaidBlock").then((module) => ({ default: module.MermaidBlock }))
);

interface AnswerMarkdownProps {
  content: string;
  citations?: Citation[];
  onOpenCitation: (c: Citation) => void;
}

const textOf = (children: unknown): string => {
  if (Array.isArray(children)) return children.map((c) => textOf(c)).join("");
  if (typeof children === "string") return children;
  return "";
};

export function AnswerMarkdown({ content, citations, onOpenCitation }: AnswerMarkdownProps) {
  // Convert every [ID:...] marker into markdown links so ReactMarkdown hands them
  // to the citation override. A marker holds either numeric indices (chat/search:
  // [ID:0], legacy [ID:0ID:1]) or engine chunk ids (agent canvas:
  // [ID:bd7636ac5bbdadce]); both resolve against the citation list. A marker that
  // resolves to nothing keeps its raw text — the previous `match(/\d+/g)` split
  // chopped chunk ids into digits and printed `ID:7636ID:5`.
  const prepped = content.replace(CITATION_TOKEN_RE, (whole, inner: string) => {
    const refs = resolveCitationTokens(citations, inner);
    if (refs.length === 0) return whole;
    // An out-of-range numeric index has nothing to link to, so the marker stays
    // verbatim; an unmatched chunk id becomes a muted chip (see below).
    if (refs.every((ref) => ref.index < 0 && /^\d+$/.test(ref.token))) return whole;
    return refs
      .map((ref) =>
        ref.index >= 0
          ? `[ID:${ref.index}](citation:${ref.index})`
          : `[ID:${ref.token.slice(0, 8)}](citation-raw:${encodeURIComponent(ref.token)})`,
      )
      .join("");
  });

  const urlTransform = (url: string) => {
    const u = String(url ?? "");
    if (/^citation:\d+$/i.test(u)) return u;
    if (/^citation-raw:[A-Za-z0-9%.-]*$/i.test(u)) return u;
    if (/^(https?:|mailto:|#|\/)/i.test(u)) return u;
    return "#";
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const components: any = {
    a: (props: any) => {
      const href = String(props?.href ?? "");
      const m = href.match(/^citation:(\d+)$/);
      if (m) {
        const idx = parseInt(m[1], 10);
        return <CitationLink index={idx} citation={citations?.[idx]} onOpen={onOpenCitation} />;
      }
      const raw = href.match(/^citation-raw:(.*)$/);
      if (raw) {
        // An engine chunk id that is not in this answer's reference list (a stale
        // reference, or a citation-less render). Show it as a muted chip instead
        // of a fake numeric citation.
        const token = decodeURIComponent(raw[1]);
        return (
          <span
            className="mx-0.5 inline-flex items-center rounded bg-muted px-1 text-xs text-muted-foreground"
            title={token}
          >
            ID:{token.slice(0, 8)}
          </span>
        );
      }
      return (
        <a
          href={href}
          className="text-primary underline"
          target={href.startsWith("http") ? "_blank" : undefined}
          rel="noreferrer"
        >
          {props.children}
        </a>
      );
    },
    pre: (props: any) => {
      const child = props?.children;
      const className = String(child?.props?.className ?? "");
      if (/\blanguage-mermaid\b/i.test(className)) {
        return (
          <Suspense fallback={<div className="my-2 h-40 animate-pulse rounded-md bg-muted/40" />}>
            <MermaidBlock code={textOf(child?.props?.children)} />
          </Suspense>
        );
      }
      return <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-muted/60 p-3">{props.children}</pre>;
    },
    table: (props: any) => (
      <div className="workbench-table-scroll overflow-x-auto">
        <table className="w-full">{props.children}</table>
      </div>
    ),
  };

  return (
    <div className="workbench-answer">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} urlTransform={urlTransform}>
        {prepped}
      </ReactMarkdown>
    </div>
  );
}
