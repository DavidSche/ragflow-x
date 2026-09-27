import { useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  fileExtension,
  isDecodedTextKind,
  officePreviewKind,
  parseDelimited,
  prettyJson,
  previewFormatKind,
} from "./document-preview";

/**
 * OriginalDocumentViewer – a unified viewer for the original document file,
 * handling Office, PDF, Markdown, HTML, CSV, JSON, XML, media, plain text and
 * a safe download fallback through the shared preview-format registry.
 *
 * Callers are responsible for fetching the blob and revoking its object URL.
 */
export function OriginalDocumentViewer({
  blobUrl,
  contentType,
  text,
  filename,
  loading,
}: {
  blobUrl?: string;
  contentType: string;
  text?: string;
  filename?: string;
  loading?: boolean;
}) {
  const officeRef = useRef<HTMLDivElement | null>(null);
  const [officeError, setOfficeError] = useState(false);
  const previewKind = previewFormatKind(contentType, filename);
  const officeKind = officePreviewKind(contentType, filename);
  const [fetchedText, setFetchedText] = useState("");
  const decodedText = text || fetchedText;

  useEffect(() => {
    if (!officeKind || !blobUrl) return;
    const container = officeRef.current;
    if (!container) return;
    let cancelled = false;
    container.innerHTML = "";
    setOfficeError(false);

    const render = async () => {
      const response = await fetch(blobUrl);
      const buffer = await response.arrayBuffer();
      if (cancelled) return;
      if (officeKind === "docx") {
        const { renderAsync } = await import("docx-preview");
        if (cancelled) return;
        await renderAsync(buffer, container, undefined, {
          inWrapper: false,
          ignoreLastRenderedPageBreak: true,
        });
      } else if (officeKind === "pptx") {
        const { init } = await import("pptx-preview");
        if (cancelled) return;
        const viewer = init(container, {
          width: container.clientWidth || 960,
          height: container.clientHeight || 540,
        });
        viewer.preview(buffer);
      } else {
        const readXlsxFile = (await import("read-excel-file/browser")).default;
        if (cancelled) return;
        const sheets = await readXlsxFile(buffer);
        sheets.forEach((sheet) => {
          const heading = document.createElement("h3");
          heading.textContent = sheet.sheet;
          heading.className = "mt-3 text-sm font-semibold";
          const table = document.createElement("table");
          table.className = "min-w-full border-collapse text-sm";
          sheet.data.forEach((row) => {
            const tableRow = document.createElement("tr");
            row.forEach((cell) => {
              const tableCell = document.createElement("td");
              tableCell.textContent = cell instanceof Date
                ? cell.toISOString()
                : String(cell ?? "");
              tableCell.className = "border px-2 py-1 align-top";
              tableRow.appendChild(tableCell);
            });
            table.appendChild(tableRow);
          });
          container.append(heading, table);
        });
      }
    };

    render().catch(() => setOfficeError(true));
    return () => {
      cancelled = true;
      container.innerHTML = "";
    };
  }, [blobUrl, contentType, filename, officeKind]);

  useEffect(() => {
    if (!isDecodedTextKind(previewKind) || !blobUrl || text) {
      setFetchedText("");
      return;
    }
    let cancelled = false;
    setFetchedText("");
    (async () => {
      const response = await fetch(blobUrl);
      const content = await response.text();
      if (!cancelled) setFetchedText(content);
    })().catch(() => {
      if (!cancelled) setFetchedText("");
    });
    return () => {
      cancelled = true;
    };
  }, [blobUrl, previewKind, text]);

  if (loading) {
    return <div className="text-muted-foreground">加载中...</div>;
  }
  if (previewKind === "docx" || previewKind === "xlsx" || previewKind === "pptx") {
    if (officeError || !blobUrl) return <DownloadFallback blobUrl={blobUrl} filename={filename} />;
    return (
      <div
        ref={officeRef}
        data-testid="office-preview"
        className="min-h-full bg-background"
      />
    );
  }
  if (!blobUrl) {
    return <div className="text-muted-foreground">无法加载原文档</div>;
  }
  if (previewKind === "image") {
    return <img src={blobUrl} alt="document preview" className="max-h-full w-full object-contain" />;
  }
  if (previewKind === "pdf") {
    return <iframe src={blobUrl} title="document preview" className="h-full min-h-[60vh] w-full border-0" />;
  }
  if (previewKind === "audio") {
    return <audio controls data-testid="document-audio" src={blobUrl} className="w-full" />;
  }
  if (previewKind === "video") {
    return <video controls data-testid="document-video" src={blobUrl} className="max-h-[60vh] w-full" />;
  }
  if (previewKind === "markdown" && decodedText) {
    return (
      <div
        data-testid="markdown-preview"
        className="max-w-none space-y-2 text-sm leading-6"
      >
        <ReactMarkdown remarkPlugins={[remarkGfm]}>
          {decodedText}
        </ReactMarkdown>
      </div>
    );
  }
  if (previewKind === "html") {
    return decodedText ? (
      <iframe
        sandbox=""
        srcDoc={decodedText}
        title="document preview"
        className="h-full min-h-[60vh] w-full border-0 bg-background"
      />
    ) : (
      <iframe sandbox="" src={blobUrl} title="document preview" className="h-full min-h-[60vh] w-full border-0 bg-background" />
    );
  }
  if (previewKind === "csv" && decodedText) {
    const rows = parseDelimited(decodedText, fileExtension(filename) === "tsv" ? "\t" : undefined);
    const [header, ...body] = rows;
    return (
      <div data-testid="csv-preview" className="overflow-x-auto">
        <table className="min-w-full border-collapse text-sm">
          <thead>
            <tr>{header?.map((cell, index) => <th key={index} className="border bg-muted/60 px-2 py-1 text-left font-semibold">{cell}</th>)}</tr>
          </thead>
          <tbody>
            {body.map((row, rowIndex) => (
              <tr key={rowIndex}>
                {row.map((cell, cellIndex) => <td key={cellIndex} className="border px-2 py-1 align-top">{cell}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }
  if (previewKind === "json" && decodedText) {
    return (
      <pre data-testid="json-preview" className="whitespace-pre-wrap break-words font-mono text-xs leading-5">
        {prettyJson(decodedText)}
      </pre>
    );
  }
  if (isDecodedTextKind(previewKind) && decodedText) {
    return <pre className="whitespace-pre-wrap break-words">{decodedText}</pre>;
  }
  return (
    <div className="flex flex-col gap-2 text-muted-foreground">
      <span>该格式不支持在线预览，可下载查看。</span>
      <a href={blobUrl} download className="text-primary underline">
        {filename || "下载"}
      </a>
    </div>
  );
}

function DownloadFallback({ blobUrl, filename }: { blobUrl?: string; filename?: string }) {
  if (!blobUrl) return <div className="text-muted-foreground">无法加载原文档</div>;
  return (
    <div className="flex flex-col gap-2 text-muted-foreground">
      <span>该格式不支持在线预览，可下载查看。</span>
      <a href={blobUrl} download className="text-primary underline">
        {filename || "下载"}
      </a>
    </div>
  );
}
