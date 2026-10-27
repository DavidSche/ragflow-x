import { memo, useMemo } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  ConnectionMode,
  type Node,
  type Edge,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { dslToGraph } from "./dslUtils";
import { nodeTypes } from "./AgentCanvas";

interface Props {
  dsl: Record<string, any>;
  className?: string;
}

/** Read-only mini ReactFlow preview for a version's workflow DSL. */
function VersionPreviewInner({ dsl, className = "" }: Props) {
  const { nodes, edges } = useMemo(() => {
    try {
      return dslToGraph(dsl);
    } catch {
      return { nodes: [] as Node[], edges: [] as Edge[] };
    }
  }, [dsl]);

  return (
    <div className={`relative rounded border bg-white ${className}`} style={{ minHeight: 220, height: 320 }}>
      <ReactFlow
        connectionMode={ConnectionMode.Loose}
        nodes={nodes}
        edges={edges}
        fitView
        nodeTypes={nodeTypes}
        edgeTypes={{}}
        zoomOnScroll={false}
        panOnDrag={false}
        zoomOnDoubleClick={false}
        preventScrolling={false}
        minZoom={0.1}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="#e2e8f0" />
      </ReactFlow>
    </div>
  );
}

export const VersionPreview = memo(function VersionPreview(props: Props) {
  return (
    <ReactFlowProvider>
      <VersionPreviewInner {...props} />
    </ReactFlowProvider>
  );
});
