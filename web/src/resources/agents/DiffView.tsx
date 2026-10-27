// DiffView — renders a structured DSL diff between the current canvas and a
// saved version. Color-coded: green = added, red = removed, amber = modified.

import type { DslDiff } from "./dslDiff";

interface Props {
  diff: DslDiff;
}

const CHANGE_STYLES = {
  added: "bg-green-50 text-green-700 border-green-200",
  removed: "bg-red-50 text-red-700 border-red-200",
  modified: "bg-amber-50 text-amber-700 border-amber-200",
} as const;

const CHANGE_ICONS = {
  added: "+",
  removed: "−",
  modified: "~",
} as const;

function Stat({ label, added, removed, modified }: { label: string; added: number; removed: number; modified: number }) {
  const total = added + removed + modified;
  if (total === 0) return null;
  return (
    <div className="flex items-center gap-1.5 text-[10px]">
      <span className="font-medium text-slate-600">{label}</span>
      {added > 0 && <span className="rounded bg-green-100 px-1 py-0.5 text-green-700">+{added}</span>}
      {removed > 0 && <span className="rounded bg-red-100 px-1 py-0.5 text-red-700">-{removed}</span>}
      {modified > 0 && <span className="rounded bg-amber-100 px-1 py-0.5 text-amber-700">~{modified}</span>}
    </div>
  );
}

export function DiffView({ diff }: Props) {
  if (diff.isEmpty) {
    return (
      <div className="flex items-center gap-2 rounded border border-green-200 bg-green-50 px-3 py-2 text-xs text-green-700">
        <span className="text-green-500">✓</span>
        <span>当前画布与该版本完全一致，无差异</span>
      </div>
    );
  }

  return (
    <div className="space-y-2 text-[11px]">
      {/* Summary stats */}
      <div className="flex flex-wrap gap-3">
        <Stat label="节点" added={diff.stats.nodesAdded} removed={diff.stats.nodesRemoved} modified={diff.stats.nodesModified} />
        <Stat label="连线" added={diff.stats.edgesAdded} removed={diff.stats.edgesRemoved} modified={diff.stats.edgesModified} />
        <Stat label="组件" added={diff.stats.componentsAdded} removed={diff.stats.componentsRemoved} modified={diff.stats.componentsModified} />
      </div>

      {/* Node changes */}
      {diff.nodes.length > 0 && (
        <div>
          <div className="mb-1 font-medium text-slate-600">节点变更</div>
          <div className="space-y-0.5">
            {diff.nodes.map((n) => (
              <div key={n.id} className={`flex items-start gap-1.5 rounded border px-2 py-1 ${CHANGE_STYLES[n.change]}`}>
                <span className="mt-0.5 shrink-0 font-mono text-[10px]">{CHANGE_ICONS[n.change]}</span>
                <div className="min-w-0 flex-1">
                  <span className="font-medium">{n.label}</span>
                  <span className="ml-1 font-mono text-[9px] opacity-60">{n.id}</span>
                  {n.change === "modified" && n.details && n.details.length > 0 && (
                    <div className="mt-0.5 flex flex-wrap gap-1">
                      {n.details.slice(0, 6).map((d, i) => (
                        <span key={i} className="rounded bg-white/60 px-1 py-0.5 text-[9px]">{d}</span>
                      ))}
                      {n.details.length > 6 && (
                        <span className="text-[9px] opacity-60">+{n.details.length - 6} more</span>
                      )}
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Edge changes */}
      {diff.edges.length > 0 && (
        <div>
          <div className="mb-1 font-medium text-slate-600">连线变更</div>
          <div className="space-y-0.5">
            {diff.edges.map((e) => (
              <div key={e.id} className={`flex items-center gap-1.5 rounded border px-2 py-1 ${CHANGE_STYLES[e.change]}`}>
                <span className="shrink-0 font-mono text-[10px]">{CHANGE_ICONS[e.change]}</span>
                <span className="font-mono text-[9px]">{e.id}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Component changes */}
      {diff.components.length > 0 && (
        <div>
          <div className="mb-1 font-medium text-slate-600">引擎组件变更</div>
          <div className="space-y-0.5">
            {diff.components.map((c) => (
              <div key={c.id} className={`flex items-start gap-1.5 rounded border px-2 py-1 ${CHANGE_STYLES[c.change]}`}>
                <span className="mt-0.5 shrink-0 font-mono text-[10px]">{CHANGE_ICONS[c.change]}</span>
                <div className="min-w-0 flex-1">
                  <span className="font-medium">{c.componentName}</span>
                  <span className="ml-1 font-mono text-[9px] opacity-60">{c.id}</span>
                  {c.change === "modified" && c.details && c.details.length > 0 && (
                    <div className="mt-0.5 flex flex-wrap gap-1">
                      {c.details.slice(0, 6).map((d, i) => (
                        <span key={i} className="rounded bg-white/60 px-1 py-0.5 text-[9px]">{d}</span>
                      ))}
                      {c.details.length > 6 && (
                        <span className="text-[9px] opacity-60">+{c.details.length - 6} more</span>
                      )}
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
