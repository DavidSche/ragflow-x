// DSL bridge for the Agent workflow canvas.
//
// The RAGFlow agent DSL has a single canonical wire shape:
//
//   {
//     "globals":    {...},
//     "graph":      { "nodes": [...], "edges": [...] },   // React-Flow
//     "variables":  [...],
//     "components": { "<nodeId>": {                        // engine topology
//       "downstream": [...], "upstream": [...],
//       "obj": { "component_name": "Name", "params": {...} }
//     }},
//     "path": [...], "retrieval": {...}, "history": [...]
//   }
//
// `graph` is the layout surface (node positions/handles); `components` is the
// topology the engine executes. On every save we rebuild `components` from
// `graph` so the two stay in lockstep, matching RAGFlow's
// `buildDslComponentsByGraph`. This module also provides connection
// validation (no self-loop / duplicate / cycle), operator metadata for the
// palette, and a layered auto-layout.

import type { Edge, Node } from "@xyflow/react";

/** RAGFlow canvas category for a regular agent canvas. */
export const AGENT_CANVAS_CATEGORY = "agent_canvas";
/** RAGFlow canvas category for a data-pipeline canvas (File → Parser …). */
export const DATAFLOW_CANVAS_CATEGORY = "dataflow_canvas";

export type CanvasCategory = string;

// ─── Operator metadata (palette + node theming) ────────────────────────

export interface OperatorMeta {
  label: string;
  color: string;
  nodeType: string;
  category: string;
  description: string;
}

const META: Record<string, OperatorMeta> = {
  Begin: { label: "Begin", color: "#16a34a", nodeType: "beginNode", category: "输入", description: "会话起点，定义对话模式与输入参数" },
  Generate: { label: "Generate", color: "#2563eb", nodeType: "generalNode", category: "大模型", description: "LLM 生成：模型、温度、提示词模板" },
  LLM: { label: "LLM", color: "#2563eb", nodeType: "generalNode", category: "大模型", description: "LLM 生成节点（别名）" },
  Knowledge: { label: "Knowledge", color: "#7c3aed", nodeType: "retrievalNode", category: "知识库", description: "从知识库检索并召回内容" },
  Retrieval: { label: "Retrieval", color: "#7c3aed", nodeType: "retrievalNode", category: "知识库", description: "知识库检索节点（别名）" },
  Categorize: { label: "Categorize", color: "#be123c", nodeType: "categorizeNode", category: "流程", description: "按规则/分类把问题路由到不同分支" },
  Chunker: { label: "Chunker", color: "#0891b2", nodeType: "chunkerNode", category: "文档", description: "文档分块：方法、块大小与重叠" },
  Tool: { label: "Tool", color: "#b45309", nodeType: "toolNode", category: "工具", description: "调用外部工具 / OpenAPI 接口" },
  Code: { label: "Code", color: "#4f46e5", nodeType: "ragNode", category: "处理", description: "执行 Python 代码片段" },
  Answer: { label: "Answer", color: "#0f766e", nodeType: "generalNode", category: "输出", description: "最终回答输出节点（末端，无后继）" },
  Switch: { label: "Switch", color: "#c2410c", nodeType: "switchNode", category: "流程", description: "条件路由：按变量值把流量分到不同分支" },
  Message: { label: "Message", color: "#0d9488", nodeType: "generalNode", category: "输出", description: "消息输出：静态文本或模板拼接" },
  RewriteQuestion: { label: "RewriteQuestion", color: "#7c3aed", nodeType: "generalNode", category: "大模型", description: "问题改写：用 LLM 优化检索查询" },
  Iteration: { label: "Iteration", color: "#b45309", nodeType: "iterationNode", category: "流程", description: "循环：对列表逐项执行子工作流" },
  IterationItem: { label: "IterationItem", color: "#b45309", nodeType: "iterationStartNode", category: "内部", description: "循环起点：迭代体内的入口节点" },
  LoopItem: { label: "LoopItem", color: "#b45309", nodeType: "loopStartNode", category: "内部", description: "循环起点（Loop 别名）" },
  ExitLoop: { label: "ExitLoop", color: "#b45309", nodeType: "exitLoopNode", category: "内部", description: "循环出口：退出迭代体" },
  Note: { label: "Note", color: "#64748b", nodeType: "generalNode", category: "辅助", description: "便签：不参与执行，仅做备注" },
  WaitingDialogue: { label: "WaitingDialogue", color: "#4338ca", nodeType: "generalNode", category: "流程", description: "等待用户输入后继续执行" },
  Agent: { label: "Agent", color: "#be185d", nodeType: "agentNode", category: "工具", description: "子 Agent：调用另一个 Agent 画布" },

  // ── Data-pipeline operators (dataflow_canvas) ─────────────────────
  File: { label: "File", color: "#16a34a", nodeType: "beginNode", category: "数据管道", description: "数据管道起点：接收上传文件" },
  Parser: { label: "Parser", color: "#0284c7", nodeType: "parserNode", category: "数据管道", description: "解析文件（PDF/Office/图片等），输出格式化内容" },
  Tokenizer: { label: "Tokenizer", color: "#0891b2", nodeType: "chunkerNode", category: "数据管道", description: "检索入口 / 混合检索分块" },
  TokenChunker: { label: "TokenChunker", color: "#0891b2", nodeType: "chunkerNode", category: "数据管道", description: "按 Token 数分块" },
  TitleChunker: { label: "TitleChunker", color: "#0891b2", nodeType: "chunkerNode", category: "数据管道", description: "按标题层级分块" },
  Extractor: { label: "Extractor", color: "#db2777", nodeType: "extractorNode", category: "数据管道", description: "按规则/字段从内容抽取信息（实体、概念、主题）" },
  Compiler: { label: "Compiler", color: "#4f46e5", nodeType: "compilerNode", category: "数据管道", description: "知识编译：把 Chunk 编译为结构化知识单元" },
  Loop: { label: "Loop", color: "#b45309", nodeType: "loopNode", category: "数据管道", description: "循环控制（筛选 / 遍历）" },
  ExcelProcessor: { label: "ExcelProcessor", color: "#047857", nodeType: "generalNode", category: "数据管道", description: "处理表格文件（Excel/CSV）" },
};

/** Palette groups rendered in the left panel. */
export const OPERATOR_PALETTE: { category: string; items: OperatorMeta[] }[] = [
  { category: "输入", items: [META.Begin] },
  { category: "大模型", items: [META.Generate, META.RewriteQuestion] },
  { category: "知识库", items: [META.Knowledge, META.Categorize] },
  { category: "文档", items: [META.Chunker] },
  { category: "流程", items: [META.Switch, META.Iteration, META.WaitingDialogue] },
  { category: "处理/工具", items: [META.Code, META.Tool, META.Agent] },
  { category: "输出", items: [META.Answer, META.Message] },
  { category: "辅助", items: [META.Note] },
];

/** Palette for data-pipeline canvases (dataflow_canvas). */
export const PIPELINE_PALETTE: { category: string; items: OperatorMeta[] }[] = [
  { category: "输入", items: [META.File] },
  { category: "数据管道", items: [META.Parser, META.Tokenizer, META.TokenChunker, META.TitleChunker, META.Extractor, META.Compiler, META.Loop, META.ExcelProcessor] },
];

/** Pick the operator palette for a canvas category (default: agent/workflow). */
export function paletteFor(category?: string) {
  return (category ?? AGENT_CANVAS_CATEGORY) === DATAFLOW_CANVAS_CATEGORY
    ? PIPELINE_PALETTE
    : OPERATOR_PALETTE;
}

/** Nodes that behave as a begin node: source-only (no upstream). */
export function isBeginLikeLabel(label?: string): boolean {
  return label === "Begin" || label === "File";
}

/** Nodes that behave as a terminal node: target-only (no downstream). */
export function isTerminalLabel(label?: string): boolean {
  return label === "Answer";
}

export function operatorMetaFor(label?: string): OperatorMeta | undefined {
  if (!label) return undefined;
  for (const k of Object.keys(META)) {
    if (label === k || label.includes(k)) return META[k];
  }
  return undefined;
}

export function nodeTypeFor(label: string): string {
  return operatorMetaFor(label)?.nodeType ?? "generalNode";
}

export function nodeColor(label?: string): string {
  return operatorMetaFor(label)?.color ?? "#64748b";
}

/**
 * Deployable operator-name mapping for saved `components[].obj.component_name`.
 *
 * Keep `ENGINE_OPERATOR_NAME_MAP` empty (identity) to target canonical RAGFlow,
 * whose executable components are Generate/Answer/Knowledge/Chunker/Code/Tool.
 * The local fork under `ragflow` renames several of them
 * (LLM/Message/Retrieval/Tokenizer/CodeExec/…) — point
 * `ENGINE_OPERATOR_NAME_MAP` at `FORK_OPERATOR_NAME_MAP` to emit names its
 * runtime (`agent/component/*.py`) resolves. The canvas `graph.data.label`
 * always keeps the friendly label; only `component_name` is remapped, so the
 * editor round-trips unchanged.
 */
export const ENGINE_OPERATOR_NAME_MAP: Record<string, string> = {};

/** Executable operator names used by the ragflow fork runtime. */
export const FORK_OPERATOR_NAME_MAP: Record<string, string> = {
  Generate: "LLM",
  Answer: "Message",
  Knowledge: "Retrieval",
  Retrieval: "Retrieval",
  Chunker: "Tokenizer",
  Code: "CodeExec",
  Tool: "Tool",
  Categorize: "Categorize",
};

export function resolveComponentName(label: string): string {
  return ENGINE_OPERATOR_NAME_MAP[label] ?? label;
}

// ─── DSL scaffolding ────────────────────────────────────────────────────

/**
 * Minimal but valid agent DSL that RAGFlow accepts when creating a brand-new
 * agent. Mirrors RAGFlow's `EmptyDsl` (single Begin node).
 */
export function createEmptyAgentDsl(): Record<string, any> {
  return {
    graph: {
      nodes: [
        {
          id: "begin",
          type: "beginNode",
          position: { x: 50, y: 200 },
          data: {
            label: "Begin",
            name: "begin",
            form: {
              mode: "conversational",
              prologue: "Hi! I'm your assistant. What can I do for you?",
            },
          },
          sourcePosition: "left",
          targetPosition: "right",
        },
      ],
      edges: [],
    },
    components: {
      begin: {
        obj: { component_name: "Begin", params: {} },
        downstream: [],
        upstream: [],
      },
    },
    retrieval: [],
    history: [],
    path: [],
    variables: [],
    globals: {
      "sys.query": "",
      "sys.user_id": "",
      "sys.conversation_turns": 0,
      "sys.files": [],
      "sys.history": [],
      "sys.date": "",
    },
  };
}

/**
 * Default DSL for a brand-new data-pipeline canvas (dataflow_canvas).
 * Mirrors RAGFlow's `DataflowEmptyDsl` and the saved default JSON:
 * a File begin node feeding a Parser node, with per-format parse setups.
 */
/** Canonical Parser node form (outputs + per-format parse setups). */
export const DATAFLOW_PARSER_FORM = {
  outputs: {
    html: { type: "string", value: "" },
    json: { type: "Array<object>", value: [] },
    markdown: { type: "string", value: "" },
    text: { type: "string", value: "" },
  },
  setups: [
    { fileFormat: "pdf", output_format: "json", parse_method: "DeepDOC", preprocess: "main_content", flatten_media_to_text: false, remove_header_footer: false, pages: [{ from: 1, to: 100000 }] },
    { fileFormat: "spreadsheet", output_format: "html", parse_method: "DeepDOC", preprocess: "main_content", flatten_media_to_text: false },
    { fileFormat: "image", output_format: "text", parse_method: "ocr", preprocess: "main_content", system_prompt: "" },
    { fileFormat: "email", fields: ["from", "to", "cc", "bcc", "date", "subject", "body", "attachments"], output_format: "text", preprocess: "main_content" },
    { fileFormat: "markdown", output_format: "json", preprocess: "main_content", flatten_media_to_text: false },
    { fileFormat: "text&code", output_format: "json", preprocess: "main_content" },
    { fileFormat: "html", output_format: "json", preprocess: "main_content", remove_header_footer: false },
    { fileFormat: "doc", output_format: "json", preprocess: "main_content", flatten_media_to_text: false, remove_header_footer: false },
    { fileFormat: "docx", output_format: "json", preprocess: "main_content", flatten_media_to_text: false, remove_header_footer: false },
    { fileFormat: "slides", output_format: "json", parse_method: "DeepDOC", preprocess: "main_content" },
  ],
};

/** Serialize-friendly deep clone of a plain JSON seed form. */
export function cloneSeedForm<T>(seed: T): T {
  return JSON.parse(JSON.stringify(seed)) as T;
}

export function createDataflowEmptyDsl(): Record<string, any> {
  const parserForm = cloneSeedForm(DATAFLOW_PARSER_FORM);
  const fileId = "File";
  const parserId = "Parser:HipSignsRhyme";
  return {
    graph: {
      nodes: [
        {
          id: fileId,
          type: "beginNode",
          position: { x: 50, y: 200 },
          data: { label: "File", name: "File" },
          sourcePosition: "left",
          targetPosition: "right",
        },
        {
          id: parserId,
          type: "parserNode",
          position: { x: 316.99524094206413, y: 195.39629819663406 },
          data: { form: parserForm, label: "Parser", name: "Parser_0" },
          sourcePosition: "right",
          targetPosition: "left",
        },
      ],
      edges: [
        {
          id: "xy-edge__Filestart-Parser:HipSignsRhymeend",
          source: fileId,
          sourceHandle: "start",
          target: parserId,
          targetHandle: "end",
        },
      ],
    },
    components: {
      [fileId]: {
        obj: { component_name: "File", params: {} },
        downstream: [parserId],
        upstream: [],
      },
      [parserId]: {
        obj: { component_name: "Parser", params: parserForm },
        downstream: [],
        upstream: [fileId],
      },
    },
    retrieval: [],
    history: [],
    path: [],
    variables: [],
    messages: [],
    globals: { "sys.history": [] },
  };
}

export function newNodeId(prefix = "node"): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`;
  }
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

// ─── DSL ↔ React-Flow bridge ───────────────────────────────────────────

export interface AgentGraph {
  nodes: Node[];
  edges: Edge[];
}

const INTERNAL_KEY = "__dslType";

/** Convert a server DSL into React-Flow state (`graph` is the source of truth). */
export function dslToGraph(dsl: Record<string, any>): AgentGraph {
  const rawNodes: any[] = Array.isArray(dsl?.graph?.nodes) ? dsl.graph.nodes : [];
  const rawEdges: any[] = Array.isArray(dsl?.graph?.edges) ? dsl.graph.edges : [];

  // Map DSL canvas node types to ReactFlow node types
  const DSL_TYPE_TO_RT: Record<string, string> = {
    iterationNode: "iterationNode",
    iterationStartNode: "iterationStartNode",
    loopStartNode: "loopStartNode",
    exitLoopNode: "exitLoopNode",
  };

  const nodes: Node[] = rawNodes.map((n) => {
    const data: Record<string, any> = { ...(n.data ?? {}) };
    data[INTERNAL_KEY] = n.type; // preserve the DSL canvas node type for export
    const dslType = String(n.type ?? "");
    const rtType = DSL_TYPE_TO_RT[dslType] ?? "agentNode";
    return {
      id: String(n.id),
      type: rtType,
      position: n.position ?? { x: 80, y: 160 },
      data,
      sourcePosition: n.sourcePosition,
      targetPosition: n.targetPosition,
      parentId: n.parentId,
      // Iteration group nodes need a minimum size
      ...(rtType === "iterationNode" ? { style: { width: 280, height: 180 } } : {}),
      // Child nodes of iteration groups should be constrained to parent bounds
      ...(n.parentId ? { extent: "parent" as const } : {}),
    } as Node;
  });
  // Normalize single-source/single-target edges to the generic handle so the
  // custom node (one source, one target handle) renders them cleanly. RAGFlow
  // data-pipeline canvases emit "start"/"end" handle ids that only matter to
  // its own canvas; the engine topology only reads source/target ids.
  const edges: Edge[] = rawEdges.map((e, i) => {
    const origSource = e.sourceHandle == null ? undefined : String(e.sourceHandle);
    const origTarget = e.targetHandle == null ? undefined : String(e.targetHandle);
    const data =
      origSource || origTarget
        ? { __dslSourceHandle: origSource ?? null, __dslTargetHandle: origTarget ?? null }
        : undefined;
    return {
      id: e.id ?? `${e.source}->${e.target}-${i}`,
      source: String(e.source),
      target: String(e.target),
      sourceHandle: nilishHandle(origSource),
      targetHandle: nilishHandle(origTarget),
      type: e.type,
      animated: !!e.animated,
      data,
    } as Edge;
  });
  return { nodes, edges };
}

/** Map RAGFlow "start"/"end" handles to plain (generic) handles. */
function nilishHandle(h: unknown): string | null {
  if (h == null) return null;
  const s = String(h);
  return s === "start" || s === "end" ? null : s;
}

/** Serialize React-Flow nodes back to RAGFlow graph nodes (strips internal key). */
function flowNodesToGraphNodes(nodes: Node[]): any[] {
  return nodes.map((n) => {
    const data: Record<string, any> = { ...(n.data ?? {}) };
    const dslType: string | undefined = data[INTERNAL_KEY];
    delete data[INTERNAL_KEY];
    return {
      id: n.id,
      type: dslType ?? nodeTypeFor(String(data.label ?? "")),
      position: n.position,
      data,
      ...(n.sourcePosition ? { sourcePosition: n.sourcePosition } : {}),
      ...(n.targetPosition ? { targetPosition: n.targetPosition } : {}),
      ...(n.parentId ? { parentId: n.parentId } : {}),
    };
  });
}

const EXCLUDED_LABELS = new Set(["Note", "Tool", "Placeholder"]);

/** File-format → suffix list, matching RAGFlow `FileTypeSuffixMap`. */
const FILE_TYPE_SUFFIX: Record<string, string[]> = {
  pdf: ["pdf"],
  spreadsheet: ["xls", "xlsx", "csv"],
  image: ["jpg", "jpeg", "png", "gif"],
  email: ["eml", "msg"],
  markdown: ["md", "markdown", "mdx"],
  "text&code": ["txt", "py", "js", "java", "c", "cpp", "h", "php", "go", "ts", "sh", "cs", "kt", "sql"],
  html: ["htm", "html"],
  doc: ["doc"],
  docx: ["docx"],
  slides: ["pptx", "ppt"],
};

/**
 * Convert the canvas Parser form (`setups` array) into the engine shape
 * RAGFlow expects (`setups` object keyed by fileFormat with `order_index`
 * and `suffix`). Matches upstream `transformParserParams`. Non-array
 * `setups` (already engine-shaped) passes through untouched.
 */
export function transformParserParams(params: Record<string, any>): Record<string, any> {
  if (!params || !Array.isArray(params?.setups)) return params;
  const setups: Record<string, any> = {};
  (params.setups as any[]).forEach((cur, index) => {
    if (!cur?.fileFormat) return;
    const base: Record<string, any> = {
      output_format: cur.output_format,
      preprocess: cur.preprocess,
      suffix: FILE_TYPE_SUFFIX[cur.fileFormat] ?? [],
    };
    for (const k of [
      "parse_method",
      "flatten_media_to_text",
      "remove_header_footer",
      "system_prompt",
      "lang",
      "vlm",
      "pages",
      "fields",
      "enable_multi_column",
      "remove_toc",
      "table_result_type",
    ]) {
      if (cur[k] !== undefined) base[k] = cur[k];
    }
    setups[cur.fileFormat] = { ...base, order_index: index };
  });
  return { ...params, setups };
}

/**
 * Rebuild the engine topology (`components`) from the React-Flow `graph`.
 * Matches RAGFlow's `buildDslComponentsByGraph`:
 *  - key = node id
 *  - params come from `node.data.form` (engine source of truth), falling back
 *    to the previous component params when untouched
 *  - component_name = node label
 *  - downstream/upstream derived from edges
 *  - parent_id preserved; Tool/Note/Placeholder excluded
 */
export function buildDslComponentsFromGraph(
  nodes: any[],
  edges: any[],
  prev: Record<string, any>,
): Record<string, any> {
  const ids = new Set(nodes.map((n) => n.id));
  const validEdges = edges.filter((e) => ids.has(e.source) && ids.has(e.target));
  const out: Record<string, any> = {};
  for (const n of nodes) {
    const label = n.data?.label ?? "Node";
    if (EXCLUDED_LABELS.has(label)) continue;
    const prevComp = prev?.[n.id];
    const rawParams = n.data?.form ?? prevComp?.obj?.params ?? {};
    let params = rawParams;

    // Operator-specific param transformations (aligned with ragflow buildDslComponentsByGraph)
    if (label === "Parser") {
      params = transformParserParams(JSON.parse(JSON.stringify(rawParams)));
    } else if (label === "Categorize") {
      // Build category_description from items + edges (matches ragflow buildCategorize)
      const nodeEdges = validEdges.filter((e) => e.source === n.id);
      const items = Array.isArray(rawParams.items) ? rawParams.items : [];
      const categoryDescription: Record<string, any> = {};
      for (const item of items) {
        if (!item.name) continue;
        const itemUuid = item.uuid;
        // Find edges whose sourceHandle matches this item's uuid
        const to = itemUuid
          ? nodeEdges
              .filter((e) => e.sourceHandle === itemUuid)
              .map((e) => e.target)
          : [];
        categoryDescription[item.name] = {
          ...item,
          to,
          examples: Array.isArray(item.examples)
            ? item.examples.map((x: any) => (typeof x === "object" && x?.value !== undefined ? x.value : x))
            : [],
        };
      }
      params = { ...rawParams, category_description: categoryDescription };
    } else if (label === "Switch") {
      // Build conditions[].to from edges (matches ragflow updateSwitchFormData)
      const nodeEdges = validEdges.filter((e) => e.source === n.id);
      const conditions = Array.isArray(rawParams.conditions) ? rawParams.conditions : [];
      const updatedConditions = conditions.map((cond: any) => {
        if (!cond) return cond;
        // Each condition may have a uuid; edges from that uuid go to targets
        const condUuid = cond.uuid;
        const to = condUuid
          ? nodeEdges
              .filter((e) => e.sourceHandle === condUuid)
              .map((e) => e.target)
          : [];
        return { ...cond, to };
      });
      // Also build end_cpn_ids (else branch)
      const elseHandle = "end_cpn_ids";
      const elseTo = nodeEdges
        .filter((e) => e.sourceHandle === elseHandle)
        .map((e) => e.target);
      params = { ...rawParams, conditions: updatedConditions, [elseHandle]: elseTo };
    }

    out[n.id] = {
      obj: {
        ...(prevComp?.obj ?? {}),
        component_name: resolveComponentName(label),
        params,
      },
      downstream: validEdges.filter((e) => e.source === n.id).map((e) => e.target),
      upstream: validEdges.filter((e) => e.target === n.id).map((e) => e.source),
      ...(n.parentId ? { parent_id: n.parentId } : {}),
    };
  }
  return out;
}

/**
 * Build a fresh DSL from the current React-Flow state. Spreads the previous
 * DSL for untouched fields (globals/variables/retrieval/history/path) and
 * rewrites `graph` + `components` in lockstep.
 */
export function graphToDsl(
  nodes: Node[],
  edges: Edge[],
  prevDsl: Record<string, any>,
): Record<string, any> {
  const prev = prevDsl ?? {};
  // Drop non-executable placeholder nodes from the emitted graph (upstream
  // `graphToDsl` filters `Operator.Placeholder` the same way).
  const keptNodes = nodes.filter(
    (n) => !EXCLUDED_LABELS.has(String((n.data as any)?.label ?? "")),
  );
  const keptIds = new Set(keptNodes.map((n) => n.id));
  const graphNodes = flowNodesToGraphNodes(keptNodes);
  const graphEdges: any[] = edges
    .filter((e) => keptIds.has(e.source) && keptIds.has(e.target))
    .map((e) => {
      const data = e.data as Record<string, any> | undefined;
      return {
        id: e.id,
        source: e.source,
        target: e.target,
        sourceHandle: data?.__dslSourceHandle ?? e.sourceHandle ?? null,
        targetHandle: data?.__dslTargetHandle ?? e.targetHandle ?? null,
        ...(e.type ? { type: e.type } : {}),
      };
    });
  const components = buildDslComponentsFromGraph(graphNodes, graphEdges, prev.components ?? {});
  return {
    ...prev,
    graph: { nodes: graphNodes, edges: graphEdges },
    components,
  };
}

/**
 * Normalize a user-imported JSON object into a renderable DSL. Reads the
 * canonical `graph` block; when `components` is absent it is rebuilt from
 * the graph (matching upstream `importDsl`). Falls back to the category
 * seed when the payload has no usable `graph.nodes`.
 */
export function importDsl(
  raw: Record<string, any>,
  isAgent: boolean,
): Record<string, any> {
  const seed = isAgent ? createEmptyAgentDsl() : createDataflowEmptyDsl();
  if (raw && Array.isArray(raw?.graph?.nodes)) {
    const nodes: any[] = raw.graph.nodes;
    const edges: any[] = Array.isArray(raw.graph.edges) ? raw.graph.edges : [];
    const components =
      raw.components ??
      buildDslComponentsFromGraph(nodes, edges, seed.components ?? {});
    return {
      ...seed,
      graph: { nodes, edges },
      components,
      retrieval: raw.retrieval ?? seed.retrieval,
      history: raw.history ?? seed.history,
      path: raw.path ?? seed.path,
      variables: raw.variables ?? seed.variables,
      globals: raw.globals ?? seed.globals,
      ...(raw.messages !== undefined ? { messages: raw.messages } : {}),
    };
  }
  return seed;
}

/**
 * Heuristically decide whether an imported JSON is an agent (workflow)
 * canvas or a data-pipeline canvas. Mirrors upstream
 * `inferIsAgentFromImport` (File begin + Parser ⇒ dataflow); defaults to
 * agent when ambiguous.
 */
export function inferIsAgentFromImport(raw: Record<string, any>): boolean {
  if (raw && Array.isArray(raw?.graph?.nodes)) {
    const labels: string[] = (raw.graph.nodes as any[]).map(
      (n: any) => n?.data?.label,
    );
    if (labels.includes("File") && labels.includes("Parser")) {
      return false;
    }
  }
  return true;
}

// ─── Validation & layout ────────────────────────────────────────────────

export interface CycleInfo {
  hasCycle: boolean;
  topologicalOrder: string[];
}

/** Kahn's algorithm — the same cycle check the backend uses. */
export function detectCycle(
  nodes: { id: string }[],
  edges: { source: string; target: string }[],
): CycleInfo {
  const adj = new Map<string, string[]>();
  const indeg = new Map<string, number>();
  for (const n of nodes) {
    adj.set(n.id, []);
    indeg.set(n.id, 0);
  }
  for (const e of edges) {
    if (!indeg.has(e.target)) continue;
    adj.get(e.source)?.push(e.target);
    indeg.set(e.target, (indeg.get(e.target) ?? 0) + 1);
  }
  const queue: string[] = [];
  for (const [id, d] of indeg) if (d === 0) queue.push(id);
  const order: string[] = [];
  while (queue.length) {
    const id = queue.shift()!;
    order.push(id);
    for (const next of adj.get(id) ?? []) {
      indeg.set(next, (indeg.get(next) ?? 1) - 1);
      if (indeg.get(next) === 0) queue.push(next);
    }
  }
  return { hasCycle: order.length !== nodes.length, topologicalOrder: order };
}

export interface ConnectCheck {
  ok: boolean;
  reason?: string;
}

/** Reject self-loops, duplicate edges, cycles, and Begin/Answer boundary breaks. */
export function validateConnection(
  source: { id: string; label?: string } | null | undefined,
  target: { id: string; label?: string } | null | undefined,
  edges: { source: string; target: string }[],
  nodes: { id: string }[],
): ConnectCheck {
  if (!source || !target) return { ok: false, reason: "缺少连线的起点或终点" };
  if (source.id === target.id) return { ok: false, reason: "不允许自环连线" };
  if (edges.some((e) => e.source === source.id && e.target === target.id)) {
    return { ok: false, reason: "两个节点之间已存在连线" };
  }
  if (isTerminalLabel(source.label)) {
    return { ok: false, reason: "Answer 是最终输出节点，不能再作为后续步骤的起点" };
  }
  if (isBeginLikeLabel(target.label)) {
    return { ok: false, reason: "Begin/File 是起点节点，不能作为其他节点的下游" };
  }
  if (detectCycle(nodes, [...edges, { source: source.id, target: target.id }]).hasCycle) {
    return { ok: false, reason: "添加此连线会形成环路（DAG 必须无环）" };
  }
  return { ok: true };
}

/** Longest-path layering for a clean left→right auto layout. */
export function computeLayeredPositions(
  nodes: { id: string }[],
  edges: { source: string; target: string }[],
): Record<string, { x: number; y: number }> {
  const layer = new Map<string, number>();
  for (const n of nodes) layer.set(n.id, 0);
  let changed = true;
  let guard = 0;
  while (changed && guard++ < nodes.length + 1) {
    changed = false;
    for (const e of edges) {
      if (!layer.has(e.source) || !layer.has(e.target)) continue;
      const next = (layer.get(e.source) ?? 0) + 1;
      if ((layer.get(e.target) ?? 0) < next) {
        layer.set(e.target, next);
        changed = true;
      }
    }
  }
  const colWidth = 300;
  const rowHeight = 140;
  const rowCount = new Map<number, number>();
  const out: Record<string, { x: number; y: number }> = {};
  for (const n of nodes) {
    const col = layer.get(n.id) ?? 0;
    const row = rowCount.get(col) ?? 0;
    rowCount.set(col, row + 1);
    out[n.id] = { x: col * colWidth, y: row * rowHeight };
  }
  return out;
}
