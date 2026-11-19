/**
 * One section body, rendered as the Markdown it actually is.
 *
 * Why this exists: section bodies are stored as `content_md`, and the model writes
 * Markdown — `**加粗**`, `- 列表`, and GFM 表格 with `|---|` separators. The reader
 * used to dump the string into a `whitespace-pre-wrap` div, so a live report showed
 * its own markup: pipes, separator rows and asterisks all printed as characters.
 * A reading surface that shows source instead of text is not a reading surface.
 *
 * Deliberate rules:
 *  - the wiki class (`workbench-answer`) is reused, not reinvented: an answer and a
 *    report section must not disagree about what a table or a list looks like;
 *  - citation markers `[n]` stay literal text. They point at the frozen evidence
 *    appendix inside the exported file, and the reader has no evidence drawer to
 *    open, so turning them into links would promise a target that does not exist;
 *  - no rendering decision may add words: whatever is not in `content_md` is not on
 *    screen.
 */
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export interface ReportSectionBodyProps {
  content: string;
  /** Test hook so a suite can point at the rendered body, not the whole section. */
  testId?: string;
}

export function ReportSectionBody({ content, testId }: ReportSectionBodyProps) {
  return (
    <div className="workbench-answer" data-testid={testId}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // GFM tables are wider than the reading column; scroll them instead of
          // letting them push the dialog sideways.
          table: (props) => (
            <div className="workbench-table-scroll overflow-x-auto">
              <table className="w-full">{props.children}</table>
            </div>
          ),
          // A body may quote a link. It opens in a new tab and drops the referrer;
          // `urlTransform` (below) keeps `javascript:` out.
          a: (props) => (
            <a
              href={String(props.href ?? "")}
              className="text-primary underline"
              target={String(props.href ?? "").startsWith("http") ? "_blank" : undefined}
              rel="noreferrer"
            >
              {props.children}
            </a>
          ),
        }}
        urlTransform={(url) => (/^(https?:|mailto:|#|\/)/i.test(String(url ?? "")) ? String(url) : "#")}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
