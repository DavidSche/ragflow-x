/**
 * AnswerMarkdown – renders assistant answer as GFM markdown with interactive
 * citation popovers for [ID:n] tokens, proper tables and mermaid diagrams.
 */
import { lazy, Suspense } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Citation } from "./workbench-types";
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
  // Convert every [ID:...] token (single or multiple IDs like [ID:0], [ID:0ID:1])
  // into markdown links so ReactMarkdown hands them to the citation override.
  const prepped = content.replace(/\[ID:\s*([^\]]*)\]/gi, (_whole, inner: string) => {
    const ids = (String(inner).match(/\d+/g) || []).map(Number).filter((n) => Number.isFinite(n));
    if (ids.length === 0) return _whole;
    return ids.map((n) => `[ID:${n}](citation:${n})`).join("");
  });

  const urlTransform = (url: string) => {
    const u = String(url ?? "");
    if (/^citation:\d+$/i.test(u)) return u;
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
