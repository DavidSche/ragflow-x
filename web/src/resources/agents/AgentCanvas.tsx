import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  useEdgesState,
  useNodesState,
  useReactFlow,
  addEdge,
  Handle,
  Position,
  MarkerType,
  NodeResizeControl,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  type NodeMouseHandler,
  type NodeProps,
  type OnNodeDrag,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useNotify } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChevronLeft, ChevronRight, LayoutGrid, Maximize2, Trash2 } from "lucide-react";
import {
  DATAFLOW_PARSER_FORM,
  cloneSeedForm,
  computeLayeredPositions,
  detectCycle,
  dslToGraph,
  graphToDsl,
  isBeginLikeLabel,
  isTerminalLabel,
  nodeColor,
  newNodeId,
  paletteFor,
  validateConnection,
} from "./dslUtils";
import { OperatorParamForm } from "./OperatorParamForm";
import { defaultParams, operatorFormFor, type OperatorFormDef } from "./operatorForms";
import { AgentIcon } from "./agentIcons";
import { PipelineOperatorForm, hasDedicatedPipelineForm } from "./PipelineForms";

/**
 * AgentCanvas — production-oriented RAGFlow agent workflow editor.
 *
 * - Left palette: click or drag-to-drop operators onto the canvas.
 * - Canvas: React-Flow with drag/custom node, connection validation
 *   (no self-loop / duplicate / cycle, Begin/Answer boundary rules),
 *   keyboard delete, MiniMap + Controls + auto layout.
 * - Right inspector: node title + operator-specific property form.
 * - DSL bridge: imports `graph` from the live RAGFlow DSL and exports a
 *   rebuilt `graph` + `components` (engine topology) on every change.
 */

interface Props {
  dsl: Record<string, any>;
  disabled?: boolean;
  onChange: (next: Record<string, any>) => void;
  category?: string;
  agentTitle?: string;
  onAgentTitleChange?: (title: string) => void;
  viewportClassName?: string;
}

export function AgentCanvas({
  dsl,
  disabled,
  onChange,
  category,
  agentTitle = "",
  onAgentTitleChange,
  viewportClassName,
}: Props) {
  return (
    <ReactFlowProvider>
      <AgentCanvasInner
        dsl={dsl}
        disabled={disabled}
        onChange={onChange}
        category={category}
        agentTitle={agentTitle}
        onAgentTitleChange={onAgentTitleChange}
        viewportClassName={viewportClassName}
      />
    </ReactFlowProvider>
  );
}

export const nodeTypes = {
  agentNode: AgentFlowNode,
  iterationNode: IterationNodeComponent,
  iterationStartNode: IterationStartNodeComponent,
  exitLoopNode: ExitLoopNodeComponent,
};

function AgentCanvasInner({
  dsl,
  disabled = false,
  onChange,
  category,
  agentTitle = "",
  onAgentTitleChange,
  viewportClassName,
}: Props) {
  const notify = useNotify();
  const reactFlow = useReactFlow();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const isDataflow = category === "dataflow_canvas";

  const seedFormFor = useCallback(
    (label: string): Record<string, any> =>
      label === "Parser" ? cloneSeedForm(DATAFLOW_PARSER_FORM) : defaultParams(label),
    [],
  );

  const initial = useMemo(() => dslToGraph(dsl), [dsl]);
  const [nodes, setNodes, onNodesChange] = useNodesState(initial.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initial.edges);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [titleDraft, setTitleDraft] = useState("");
  const [formValues, setFormValues] = useState<Record<string, any>>({});
  const [formDef, setFormDef] = useState<OperatorFormDef | undefined>(undefined);
  const [paletteCollapsed, setPaletteCollapsed] = useState(false);

  const prevDslRef = useRef<Record<string, any>>(dsl);
  const mountedRef = useRef(false);

  // Re-import when the outer dsl changes from an external reload (e.g. AgentShow
  // refreshes the detail after save). Our own emits are skipped via the ref.
  useEffect(() => {
    if (!mountedRef.current) {
      mountedRef.current = true;
      prevDslRef.current = dsl;
      return;
    }
    if (prevDslRef.current === dsl) return;
    const graph = dslToGraph(dsl);
    setNodes(graph.nodes);
    setEdges(graph.edges);
    setSelectedId(null);
    prevDslRef.current = dsl;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dsl, setNodes, setEdges]);

  const emit = useCallback(
    (nds: Node[], eds: Edge[]) => {
      const next = graphToDsl(nds, eds, prevDslRef.current);
      prevDslRef.current = next;
      onChange(next);
    },
    [onChange],
  );

  const cycleInfo = useMemo(() => detectCycle(nodes, edges), [nodes, edges]);

  // ─── graph mutations ─────────────────────────────────────────────────

  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => onNodesChange(changes),
    [onNodesChange],
  );

  const onConnect = useCallback(
    (conn: Connection) => {
      if (disabled) return;
      const sourceNode = nodes.find((n) => n.id === conn.source);
      const targetNode = nodes.find((n) => n.id === conn.target);
      const check = validateConnection(sourceNode, targetNode, edges, nodes);
      if (!check.ok) {
        notify(check.reason ?? "无法添加连线", { type: "error" });
        return;
      }
      const edge: Edge = {
        id: `${conn.source}->${conn.target}-${Date.now()}`,
        source: conn.source!,
        target: conn.target!,
        sourceHandle: conn.sourceHandle,
        targetHandle: conn.targetHandle,
        animated: true,
      };
      const nextEdges = addEdge(edge, edges);
      setEdges(nextEdges);
      emit(nodes, nextEdges);
    },
    [disabled, nodes, edges, notify, emit, setEdges],
  );

  const onNodeDragStop: OnNodeDrag = useCallback(() => {
    if (disabled) return;
    emit(reactFlow.getNodes(), reactFlow.getEdges());
  }, [disabled, emit, reactFlow]);

  const handleNodesDelete = useCallback(() => {
    if (disabled) return;
    setSelectedId(null);
    setFormDef(undefined);
    emit(reactFlow.getNodes(), reactFlow.getEdges());
  }, [disabled, emit, reactFlow, setSelectedId]);

  const handleEdgesDelete = useCallback(() => {
    if (disabled) return;
    emit(reactFlow.getNodes(), reactFlow.getEdges());
  }, [disabled, emit, reactFlow]);

  const addOperator = useCallback(
    (label: string) => {
      if (disabled) return;
      const center = reactFlow.screenToFlowPosition({
        x: window.innerWidth / 2,
        y: window.innerHeight / 2,
      });
      const id = newNodeId("node");

      // Iteration nodes are group containers: create the container + a child IterationItem start node
      if (label === "Iteration") {
        const startId = newNodeId("start");
        const iterationNode: Node = {
          id,
          type: "iterationNode",
          position: { x: center.x - 140, y: center.y - 90 },
          data: {
            label,
            name: "iteration",
            form: seedFormFor(label),
          },
          style: { width: 280, height: 180 },
        };
        const startNode: Node = {
          id: startId,
          type: "iterationStartNode",
          position: { x: 20, y: 40 },
          parentId: id,
          extent: "parent" as const,
          data: {
            label: "IterationItem",
            name: "iteration_item",
            form: {},
          },
        };
        const next = [...nodes, iterationNode, startNode];
        setNodes(next);
        setSelectedId(id);
        setTitleDraft(label);
        const def = operatorFormFor(label);
        setFormDef(def);
        setFormValues({ ...seedFormFor(label) });
        emit(next, edges);
        return;
      }

      const newNode: Node = {
        id,
        type: "agentNode",
        position: { x: center.x - 90, y: center.y - 30 },
        data: {
          label,
          name: label.toLowerCase().replace(/\s+/g, "_"),
          form: seedFormFor(label),
        },
      };
      const next = [...nodes, newNode];
      setNodes(next);
      setSelectedId(id);
      setTitleDraft(label);
      const def = operatorFormFor(label);
      setFormDef(def);
      setFormValues({ ...seedFormFor(label) });
      emit(next, edges);
    },
    [disabled, nodes, edges, emit, reactFlow, setNodes],
  );

  const onDrop = useCallback(
    (ev: DragEvent<HTMLDivElement>) => {
      ev.preventDefault();
      if (disabled) return;
      const label = ev.dataTransfer.getData("application/x-ragflow-x-operator");
      if (!label) return;
      const pos = reactFlow.screenToFlowPosition({ x: ev.clientX, y: ev.clientY });
      const id = newNodeId("node");
      const newNode: Node = {
        id,
        type: "agentNode",
        position: { x: pos.x - 90, y: pos.y - 30 },
        data: {
          label,
          name: label.toLowerCase().replace(/\s+/g, "_"),
          form: seedFormFor(label),
        },
      };
      const next = [...nodes, newNode];
      setNodes(next);
      setSelectedId(id);
      setTitleDraft(label);
      const def = operatorFormFor(label);
      setFormDef(def);
      setFormValues({ ...seedFormFor(label) });
      emit(next, edges);
    },
    [disabled, nodes, edges, emit, reactFlow, setNodes],
  );

  const deleteSelected = useCallback(() => {
    if (!selectedId || disabled) return;
    const nds = nodes.filter((n) => n.id !== selectedId);
    const eds = edges.filter((e) => e.source !== selectedId && e.target !== selectedId);
    setNodes(nds);
    setEdges(eds);
    setSelectedId(null);
    setFormDef(undefined);
    emit(nds, eds);
  }, [selectedId, disabled, nodes, edges, setNodes, setEdges, emit, setSelectedId]);

  const autoLayout = useCallback(() => {
    if (disabled) return;
    const positions = computeLayeredPositions(nodes, edges);
    const nds = nodes.map((n) => ({ ...n, position: positions[n.id] ?? n.position }));
    setNodes(nds);
    emit(nds, edges);
    setTimeout(() => reactFlow.fitView({ padding: 0.2, duration: 300 }), 50);
  }, [disabled, nodes, edges, setNodes, emit, reactFlow]);

  // ─── inspector ───────────────────────────────────────────────────────

  const onNodeClick: NodeMouseHandler = useCallback(
    (_, node) => {
      if (disabled) return;
      const label = String(node.data?.label ?? "");
      const def = operatorFormFor(label);
      setFormDef(def);
      const existing: Record<string, any> = node.data?.form ?? {};
      const seed: Record<string, any> = { ...defaultParams(label) };
      for (const f of def?.fields ?? []) {
        if (f.name in existing) seed[f.name] = existing[f.name];
      }
      for (const k of Object.keys(existing)) {
        if (!(k in seed)) seed[k] = existing[k];
      }
      setFormValues(seed);
      setTitleDraft(typeof node.data?.title === "string" ? node.data.title : label);
      setSelectedId(node.id);
    },
    [disabled],
  );

  const applyDraft = useCallback(() => {
    if (!selectedId || disabled) return;
    const nds = nodes.map((n) =>
      n.id === selectedId
        ? {
            ...n,
            data: {
              ...n.data,
              ...(titleDraft.trim() ? { title: titleDraft.trim() } : {}),
              form: { ...((n.data?.form as Record<string, any>) ?? {}), ...formValues },
            },
          }
        : n,
    );
    setNodes(nds);
    emit(nds, edges);
  }, [selectedId, disabled, nodes, edges, titleDraft, formValues, setNodes, emit]);

  const selectedNode = useMemo(
    () => nodes.find((n) => n.id === selectedId) ?? null,
    [nodes, selectedId],
  );

  // ─── render ──────────────────────────────────────────────────────────

  return (
    <div className={`grid gap-3 ${paletteCollapsed ? "lg:grid-cols-[2.5rem_minmax(0,1fr)_18rem]" : "lg:grid-cols-[15rem_minmax(0,1fr)_18rem]"}`}>
      {/* Operator palette */}
      {paletteCollapsed ? (
        <aside className="flex flex-col items-center gap-1 overflow-y-auto rounded border bg-background p-1.5">
          <button
            type="button"
            onClick={() => setPaletteCollapsed(false)}
            title={isDataflow ? "展开组件库" : "展开算子库"}
            className="mb-1 flex size-8 items-center justify-center rounded border hover:bg-muted"
          >
            <ChevronRight className="size-4" aria-hidden="true" />
          </button>
          {paletteFor(category).flatMap((g) => g.items).map((op) => (
            <button
              key={op.label}
              type="button"
              disabled={disabled}
              onClick={() => addOperator(op.label)}
              title={op.description}
              className="flex size-8 items-center justify-center rounded border hover:bg-muted disabled:opacity-50"
            >
              <AgentIcon label={op.label} color={op.color} className="size-4" />
            </button>
          ))}
        </aside>
      ) : (
        <aside className="overflow-y-auto rounded border bg-background p-2">
          <div className="flex items-center justify-between gap-2 px-1 pb-2">
            <span className="text-xs font-semibold text-muted-foreground">
              {isDataflow ? "组件库" : "算子库"}（点击或拖入）
            </span>
            <button
              type="button"
              onClick={() => setPaletteCollapsed(true)}
              title={isDataflow ? "收起组件库" : "收起算子库"}
              className="rounded p-0.5 hover:bg-muted"
            >
              <ChevronLeft className="size-4" aria-hidden="true" />
            </button>
          </div>
          {paletteFor(category).map((group) => (
            <div key={group.category} className="mb-2">
              <div className="px-1 py-0.5 text-[11px] uppercase tracking-wide text-muted-foreground">
                {group.category}
              </div>
              <div className="flex flex-col gap-1">
                {group.items.map((op) => (
                  <button
                    key={op.label}
                    type="button"
                    disabled={disabled}
                    draggable={!disabled}
                    onDragStart={(e) => {
                      e.dataTransfer.setData("application/x-ragflow-x-operator", op.label);
                      e.dataTransfer.effectAllowed = "copy";
                    }}
                    onClick={() => addOperator(op.label)}
                    title={op.description}
                    className="flex items-center gap-2.5 rounded border px-2 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50"
                  >
                    <AgentIcon label={op.label} color={op.color} className="size-4" />
                    <span className="font-medium">{op.label}</span>
                  </button>
                ))}
              </div>
            </div>
          ))}
        </aside>
      )}

      {/* Toolbar + canvas */}
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded border px-2 py-1.5 text-xs text-muted-foreground">
          <span>节点 {nodes.length}</span>
          <span>连线 {edges.length}</span>
          <span className={cycleInfo.hasCycle ? "font-semibold text-red-500" : "text-green-600"}>
            {cycleInfo.hasCycle ? "存在环路" : "无环合法"}
          </span>
          <div className="ml-auto flex gap-1">
            <Button
              variant="outline"
              size="sm"
              onClick={() => reactFlow.fitView({ padding: 0.2, duration: 300 })}
              disabled={disabled}
            >
              <Maximize2 className="size-3.5" aria-hidden="true" />
              适应视图
            </Button>
            <Button variant="outline" size="sm" onClick={autoLayout} disabled={disabled}>
              <LayoutGrid className="size-3.5" aria-hidden="true" />
              自动布局
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={deleteSelected}
              disabled={disabled || !selectedId}
              className="text-destructive"
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              删除
            </Button>
          </div>
        </div>

        <div
          ref={wrapperRef}
          className={`${viewportClassName ?? "h-[520px]"} min-h-[420px] overflow-hidden rounded border bg-background`}
          onDrop={onDrop}
          onDragOver={(e) => {
            if (!disabled) e.preventDefault();
          }}
        >
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={handleNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={onNodeClick}
            onPaneClick={() => setSelectedId(null)}
            onConnect={onConnect}
            onNodeDragStop={onNodeDragStop}
            onNodesDelete={handleNodesDelete}
            onEdgesDelete={handleEdgesDelete}
            fitView
            fitViewOptions={{ padding: 0.2 }}
            nodesDraggable={!disabled}
            nodesConnectable={!disabled}
            elementsSelectable={!disabled}
            deleteKeyCode={disabled ? null : ["Backspace", "Delete"]}
            proOptions={{ hideAttribution: true }}
            defaultEdgeOptions={{
              animated: true,
              style: { stroke: "#94a3b8", strokeWidth: 1.5 },
              markerEnd: { type: MarkerType.ArrowClosed, color: "#94a3b8" },
            }}
          >
            <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
            <Controls showInteractive={!disabled} />
            <MiniMap
              pannable
              zoomable
              nodeColor={(n) => nodeColor(String((n.data as any)?.label ?? ""))}
            />
          </ReactFlow>
        </div>
      </div>

      {/* Inspector */}
      <aside className="space-y-3 rounded border bg-background p-3">
        <div className="flex items-center justify-between">
          <Label className="text-sm">{selectedNode ? "节点属性" : isDataflow ? "管道属性" : "智能体属性"}</Label>
          {selectedNode ? (
            <span
              className="text-[11px] font-medium"
              style={{ color: nodeColor(String((selectedNode.data as any)?.label ?? "")) }}
            >
              {String((selectedNode.data as any)?.label ?? "")}
            </span>
          ) : null}
        </div>
        {selectedNode ? (
          <>
            <div className="space-y-1">
              <Label className="text-xs">标题</Label>
              <Input
                className="h-8 text-sm"
                value={titleDraft}
                onChange={(e) => setTitleDraft(e.target.value)}
                disabled={disabled}
              />
            </div>
            {hasDedicatedPipelineForm(String((selectedNode.data as any)?.label ?? "")) ? (
              <PipelineOperatorForm
                label={String((selectedNode.data as any)?.label ?? "")}
                values={formValues}
                disabled={disabled}
                onChange={(name, value) => setFormValues((p) => ({ ...p, [name]: value }))}
              />
            ) : (
              <OperatorParamForm
                fields={formDef?.fields ?? []}
                values={formValues}
                disabled={disabled}
                onChange={(name, value) => setFormValues((p) => ({ ...p, [name]: value }))}
              />
            )}
            <div className="grid grid-cols-2 gap-2">
              <Button className="w-full" onClick={applyDraft} disabled={disabled}>
                应用属性
              </Button>
              <Button
                variant="destructive"
                className="w-full"
                onClick={deleteSelected}
                disabled={disabled}
              >
                <Trash2 className="size-3" aria-hidden="true" />
                删除
              </Button>
            </div>
          </>
        ) : (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <AgentIcon label="Agent" color="#64748b" className="size-5" />
              <span className="text-xs text-muted-foreground">{isDataflow ? "管道级配置" : "智能体级配置"}</span>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">名称</Label>
              <Input
                className="h-8 text-sm"
                value={agentTitle}
                disabled={disabled}
                onChange={(e) => onAgentTitleChange?.(e.target.value)}
                placeholder="智能体名称"
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {isDataflow
                ? "从左侧组件库点击或拖拽添加节点；拖动节点把手连线；选中节点后在右侧编辑属性。"
                : "从左侧算子库点击或拖拽添加节点；拖动节点把手连线；选中节点后在右侧编辑组件属性。"}
            </p>
          </div>
        )}
      </aside>
    </div>
  );
}

function AgentFlowNode({ data, selected }: NodeProps) {
  const label = String((data as any)?.label ?? "Node");
  const color = nodeColor(label);
  const isBegin = isBeginLikeLabel(label);
  const isAnswer = isTerminalLabel(label);
  const form = (data as any)?.form ?? {};

  // Dynamic output handles for Categorize (one per category item)
  const isCategorize = label === "Categorize";
  const categorizeItems: any[] = Array.isArray(form.items) ? form.items : [];

  // Dynamic output handles for Switch (one per condition + else)
  const isSwitch = label === "Switch";
  const switchConditions: any[] = Array.isArray(form.conditions) ? form.conditions : [];

  return (
    <div
      className="min-w-40 rounded-lg border bg-white px-3 py-2 text-slate-800 shadow-sm"
      style={{
        borderColor: selected ? color : "#e2e8f0",
        borderTop: `4px solid ${color}`,
        boxShadow: selected ? `0 0 0 2px ${color}33` : undefined,
      }}
    >
      {!isBegin && (
        <Handle type="target" position={Position.Left} className="!h-2.5 !w-2.5 !border !border-slate-300 !bg-slate-400" />
      )}
      <div className="text-sm font-medium" title={(data as any)?.title || label}>
        {(data as any)?.title || label}
      </div>
      <span
        className="mt-0.5 inline-block rounded px-1.5 py-0.5 text-[10px] font-medium text-white"
        style={{ background: color }}
      >
        {label}
      </span>

      {/* Form summary for Begin node */}
      {isBegin && form.mode && (
        <div className="mt-1.5 text-[10px] text-slate-500">
          <span className="rounded bg-slate-100 px-1 py-0.5">{form.mode}</span>
          {Array.isArray(form.inputs) && form.inputs.length > 0 && (
            <div className="mt-1 space-y-0.5">
              {form.inputs.slice(0, 3).map((inp: any, i: number) => (
                <div key={i} className="flex items-center gap-1 truncate text-[9px]">
                  <span className="text-slate-400">▸</span>
                  <span className="font-mono text-slate-500">{inp.key || inp.name}</span>
                  <span className="text-slate-400">:</span>
                  <span className="text-slate-400">{inp.type}</span>
                </div>
              ))}
              {form.inputs.length > 3 && (
                <div className="text-[9px] text-slate-400">…+{form.inputs.length - 3}</div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Form summary for Knowledge/Retrieval node */}
      {(label === "Knowledge" || label === "Retrieval") && (
        <div className="mt-1.5 space-y-0.5 text-[10px] text-slate-500">
          {form.retrieval_from && (
            <div><span className="text-slate-400">来源:</span> {form.retrieval_from === "memory" ? "记忆" : "知识库"}</div>
          )}
          {Array.isArray(form.kb_ids) && form.kb_ids.length > 0 && (
            <div className="truncate"><span className="text-slate-400">KB:</span> {form.kb_ids.length} 个</div>
          )}
          {form.top_n && (
            <div><span className="text-slate-400">TopN:</span> {form.top_n}</div>
          )}
        </div>
      )}

      {/* Form summary for Generate/LLM node */}
      {(label === "Generate" || label === "LLM") && form.llm_id && (
        <div className="mt-1.5 text-[10px] text-slate-500 truncate">
          <span className="text-slate-400">模型:</span> {form.llm_id}
        </div>
      )}

      {/* Form summary for Categorize node: show items as labels */}
      {isCategorize && categorizeItems.length > 0 && (
        <div className="mt-1.5 space-y-1">
          {categorizeItems.map((item: any, i: number) => (
            <div key={item.uuid || i} className="relative">
              <div className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600 truncate">
                {item.name || `分支 ${i + 1}`}
              </div>
              <Handle
                type="source"
                position={Position.Right}
                id={item.uuid}
                className="!h-2 !w-2 !border !border-slate-300 !bg-slate-400"
                style={{ top: `${24 + i * 24}px` }}
              />
            </div>
          ))}
        </div>
      )}

      {/* Form summary for Switch node: show conditions */}
      {isSwitch && (
        <div className="mt-1.5 space-y-1">
          {switchConditions.map((cond: any, i: number) => (
            <div key={cond.uuid || i} className="relative">
              <div className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600 truncate">
                {i === 0 ? "If" : i === switchConditions.length - 1 ? "Else" : "ElseIf"}
                {cond.items?.[0]?.operator && (
                  <span className="ml-1 text-slate-400">{cond.items[0].operator}</span>
                )}
              </div>
              <Handle
                type="source"
                position={Position.Right}
                id={cond.uuid}
                className="!h-2 !w-2 !border !border-slate-300 !bg-slate-400"
                style={{ top: `${24 + i * 24}px` }}
              />
            </div>
          ))}
          {/* Else handle */}
          <div className="relative">
            <div className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600">
              Else
            </div>
            <Handle
              type="source"
              position={Position.Right}
              id="end_cpn_ids"
              className="!h-2 !w-2 !border !border-slate-300 !bg-slate-400"
              style={{ top: `${24 + switchConditions.length * 24}px` }}
            />
          </div>
        </div>
      )}

      {/* Form summary for Message node */}
      {label === "Message" && Array.isArray(form.content) && form.content.length > 0 && (
        <div className="mt-1.5 text-[10px] text-slate-500 truncate">
          {String(form.content[0]).slice(0, 40)}{String(form.content[0]).length > 40 ? "…" : ""}
        </div>
      )}

      {/* Form summary for Code node */}
      {label === "Code" && form.language && (
        <div className="mt-1.5 text-[10px] text-slate-500">
          <span className="rounded bg-slate-100 px-1 py-0.5 font-mono">{form.language}</span>
        </div>
      )}

      {!isAnswer && !isCategorize && !isSwitch && (
        <Handle type="source" position={Position.Right} className="!h-2.5 !w-2.5 !border !border-slate-300 !bg-slate-400" />
      )}
    </div>
  );
}

// ─── Iteration sub-canvas node types ───────────────────────────────────

/**
 * IterationNodeComponent — a group-like container node.
 * Renders a header bar at the top and a resizeable body area.
 * Child nodes (with parentId = this node's id) are rendered inside
 * by ReactFlow's built-in group node support.
 */
function IterationNodeComponent({ id, data, selected }: NodeProps) {
  const color = nodeColor("Iteration");
  const form = (data as any)?.form ?? {};
  return (
    <div
      className="relative flex flex-col rounded-lg border bg-white/60 shadow-sm"
      style={{
        borderColor: selected ? color : "#e2e8f0",
        minHeight: 180,
        minWidth: 280,
      }}
    >
      {/* Header bar */}
      <div
        className="flex items-center gap-2 rounded-t-lg px-3 py-1.5 text-xs font-medium text-white"
        style={{ background: color }}
      >
        <span>↻ Iteration</span>
        {form.items_ref && (
          <span className="ml-auto truncate text-[10px] text-white/70 max-w-[120px]">
            {form.items_ref}
          </span>
        )}
      </div>
      {/* Body area — child nodes rendered here by ReactFlow */}
      <div className="relative flex-1 p-2">
        {/* Target handle on left */}
        <Handle type="target" position={Position.Left} className="!h-2.5 !w-2.5 !border !border-slate-300 !bg-slate-400" />
        {/* Source handle on right */}
        <Handle type="source" position={Position.Right} className="!h-2.5 !w-2.5 !border !border-slate-300 !bg-slate-400" />
        {/* Hint text when empty */}
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-[10px] text-slate-300">
          拖入节点到循环体
        </div>
      </div>
    </div>
  );
}

/**
 * IterationStartNodeComponent — the entry point inside an Iteration container.
 * This is the "IterationItem" / "LoopItem" that marks where the loop body begins.
 */
function IterationStartNodeComponent({ data, selected }: NodeProps) {
  return (
    <div
      className="flex items-center gap-1.5 rounded border bg-white px-2 py-1 text-[10px] text-slate-600 shadow-sm"
      style={{
        borderColor: selected ? "#b45309" : "#e2e8f0",
        minWidth: 80,
      }}
    >
      <span className="text-[10px]">▸</span>
      <span className="font-medium">{(data as any)?.title || "开始"}</span>
      <Handle type="source" position={Position.Right} className="!h-2 !w-2 !border !border-slate-300 !bg-slate-400" />
    </div>
  );
}

/**
 * ExitLoopNodeComponent — the exit point inside an Iteration container.
 * Marks where the loop body ends.
 */
function ExitLoopNodeComponent({ data, selected }: NodeProps) {
  return (
    <div
      className="flex items-center gap-1.5 rounded border bg-white px-2 py-1 text-[10px] text-slate-600 shadow-sm"
      style={{
        borderColor: selected ? "#b45309" : "#e2e8f0",
        minWidth: 80,
      }}
    >
      <Handle type="target" position={Position.Left} className="!h-2 !w-2 !border !border-slate-300 !bg-slate-400" />
      <span className="font-medium">{(data as any)?.title || "退出"}</span>
    </div>
  );
}
