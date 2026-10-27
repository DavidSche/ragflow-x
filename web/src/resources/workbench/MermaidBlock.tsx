/**
 * MermaidBlock – renders a ```mermaid fenced code block into an SVG on demand.
 *
 * Mermaid is loaded lazily so answering stays fast; the diagram is drawn with
 * a strict security level because the source text is model-generated.
 */
import { useEffect, useRef, useState } from "react";

export function MermaidBlock({ code }: { code: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string>("");

  useEffect(() => {
    let cancelled = false;
    const host = ref.current;
    if (!host) return;
    const diagramId = `mermaid-${Math.random().toString(36).slice(2, 10)}`;
    const theme = document.documentElement.classList.contains("dark") ? "dark" : "default";

    (async () => {
      try {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const mod: any = await import("mermaid");
        const mermaid = mod.default ?? mod;
        mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme });
        const { svg } = await mermaid.render(diagramId, code);
        if (!cancelled && ref.current) {
          ref.current.innerHTML = svg;
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [code]);

  return (
    <div className="my-2">
      {error ? (
        <div className="overflow-x-auto rounded-md border bg-muted/40 p-3">
          <pre className="whitespace-pre-wrap text-xs">{code}</pre>
          <p className="mt-2 text-xs text-destructive">Mermaid 渲染失败：{error}</p>
        </div>
      ) : (
        <div ref={ref} className="workbench-mermaid overflow-x-auto" />
      )}
    </div>
  );
}
