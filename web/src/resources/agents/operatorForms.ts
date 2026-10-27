export type FieldType =
  | "text"
  | "textarea"
  | "number"
  | "slider"
  | "select"
  | "switch"
  | "jsonArray";

export interface FieldDef {
  name: string;
  label: string;
  type: FieldType;
  default?: any;
  options?: { value: string; label: string }[];
  min?: number;
  max?: number;
  step?: number;
  placeholder?: string;
  help?: string;
}

export interface OperatorFormDef {
  label: string;
  fields: FieldDef[];
}

const LLM_FIELDS: FieldDef[] = [
  { name: "llm_id", label: "LLM 模型", type: "text", default: "", placeholder: "tenant_model_id" },
  { name: "parameter", label: "预置参数", type: "select", default: "Custom", options: [
    { value: "creativity", label: "创作（Creativity）" },
    { value: "precision", label: "严谨（Precision）" },
    { value: "balance", label: "均衡（Balance）" },
    { value: "Custom", label: "自定义" },
  ] },
  { name: "temperature", label: "温度", type: "slider", default: 0.1, min: 0, max: 1, step: 0.01 },
  { name: "top_p", label: "Top P", type: "slider", default: 0.3, min: 0, max: 1, step: 0.01 },
  { name: "presence_penalty", label: "存在惩罚", type: "slider", default: 0.4, min: -2, max: 2, step: 0.1 },
  { name: "frequency_penalty", label: "频率惩罚", type: "slider", default: 0.7, min: -2, max: 2, step: 0.1 },
  { name: "max_tokens", label: "最大 Token", type: "number", default: 256, min: 16, max: 4096 },
  { name: "thinking", label: "思考模式", type: "select", default: "default", options: [
    { value: "default", label: "默认" },
    { value: "enabled", label: "启用" },
    { value: "disabled", label: "禁用" },
  ] },
  { name: "system", label: "系统提示词", type: "textarea", default: "" },
  { name: "prompt", label: "提示词模板", type: "textarea", default: "" },
];

const KNOWLEDGE_FIELDS: FieldDef[] = [
  { name: "retrieval_from", label: "检索来源", type: "select", default: "dataset", options: [
    { value: "dataset", label: "知识库" },
    { value: "memory", label: "记忆" },
  ] },
  { name: "kb_ids", label: "知识库 ID 列表", type: "jsonArray", default: [], help: "JSON 数组：知识库 ID 列表" },
  { name: "query", label: "查询变量", type: "text", default: "{sys.query}", placeholder: "{sys.query}" },
  { name: "top_n", label: "Top N", type: "number", default: 8, min: 1, max: 20 },
  { name: "top_k", label: "Top K", type: "number", default: 1024, min: 1, max: 4096 },
  { name: "similarity_threshold", label: "相似度阈值", type: "slider", default: 0.2, min: 0, max: 1, step: 0.01 },
  { name: "keywords_similarity_weight", label: "关键词相似度权重", type: "slider", default: 0.7, min: 0, max: 1, step: 0.01 },
  { name: "vector_similarity_weight", label: "向量相似度权重", type: "slider", default: 0.3, min: 0, max: 1, step: 0.01 },
  { name: "rerank_id", label: "重排模型", type: "text", default: "" },
  { name: "cross_languages", label: "跨语言检索", type: "jsonArray", default: [], help: "JSON 数组：[\"English\", \"Chinese\"]" },
  { name: "empty_response", label: "空结果回复", type: "textarea", default: "" },
  { name: "outputs", label: "输出定义", type: "jsonArray", default: {}, help: "JSON 对象：{\"formulated_content\": {type, value}, \"json\": {type, value}}" },
];

const BEGIN_FIELDS: FieldDef[] = [
  { name: "mode", label: "对话模式", type: "select", default: "conversational", options: [
    { value: "conversational", label: "对话式" },
    { value: "task", label: "任务式" },
    { value: "webhook", label: "Webhook" },
  ] },
  { name: "enablePrologue", label: "开场白", type: "switch", default: false },
  { name: "prologue", label: "开场白内容", type: "textarea", default: "" },
  { name: "layout_recognize", label: "版面识别", type: "select", default: "", options: [
    { value: "", label: "不启用" },
    { value: "default", label: "默认" },
  ] },
  { name: "inputs", label: "输入参数", type: "jsonArray", default: [], help: "JSON 数组：[{key, type, value, optional, name, options}]" },
];

const CHUNKER_FIELDS: FieldDef[] = [
  { name: "chunk_method", label: "分块方法", type: "select", default: "naive", options: [
    { value: "naive", label: "通用" },
    { value: "fqa", label: "问答" },
    { value: "paper", label: "论文" },
    { value: "book", label: "图书" },
    { value: "laws", label: "法规" },
    { value: "manual", label: "手册" },
    { value: "table", label: "表格" },
    { value: "one", label: "单条" },
    { value: "email", label: "邮件" },
  ] },
  { name: "chunk_token_num", label: "分块 Token 数", type: "number", default: 128, min: 1, max: 1024 },
  { name: "overlap", label: "重叠 Token 数", type: "number", default: 16, min: 0, max: 100 },
  { name: "delimiter", label: "分隔符", type: "textarea", default: "\\n!?;。；！？" },
  { name: "html4excel", label: "Excel 转 HTML", type: "switch", default: false },
];

const TOOL_FIELDS: FieldDef[] = [
  { name: "tools", label: "工具列表", type: "jsonArray", default: [], help: "JSON 数组，例如 [“arxiv”, “email”]" },
  { name: "server_url", label: "OpenAPI 地址", type: "text", default: "" },
  { name: "method", label: "请求方法", type: "select", default: "GET", options: ["GET", "POST", "PUT", "DELETE"].map((m) => ({ value: m, label: m })) },
  { name: "headers", label: "请求头", type: "jsonArray", default: {}, help: "JSON 对象，例如 {\"Authorization\": \"Bearer xxx\"}" },
];

const CODE_FIELDS: FieldDef[] = [
  { name: "language", label: "语言", type: "select", default: "python", options: [{ value: "python", label: "Python" }] },
  { name: "code", label: "代码", type: "textarea", default: "" },
  { name: "timeout", label: "超时（秒）", type: "number", default: 60, min: 1, max: 600 },
  { name: "max_tokens", label: "最大 Token", type: "number", default: 512, min: 16, max: 8192 },
];

const CATEGORIZE_FIELDS: FieldDef[] = [
  { name: "query", label: "查询变量", type: "text", default: "{sys.query}", placeholder: "{sys.query}" },
  { name: "llm_id", label: "LLM 模型", type: "text", default: "", placeholder: "tenant_model_id" },
  { name: "parameter", label: "预置参数", type: "select", default: "Custom", options: [
    { value: "creativity", label: "创作（Creativity）" },
    { value: "precision", label: "严谨（Precision）" },
    { value: "balance", label: "均衡（Balance）" },
    { value: "Custom", label: "自定义" },
  ] },
  { name: "temperature", label: "温度", type: "slider", default: 0.1, min: 0, max: 1, step: 0.01 },
  { name: "message_history_window_size", label: "历史窗口", type: "number", default: 1, min: 0, max: 50 },
  { name: "description", label: "分类说明", type: "textarea", default: "" },
  { name: "items", label: "分类项", type: "jsonArray", default: [], help: "JSON 数组：[{name, to, examples}]" },
  { name: "outputs", label: "输出定义", type: "jsonArray", default: {}, help: "JSON 对象：{\"category_name\": {type: \"string\"}}" },
];

// ─── Data-pipeline operators (dataflow_canvas) ─────────────────────────

const FILE_FIELDS: FieldDef[] = [
  { name: "outputs", label: "输出参数", type: "jsonArray", default: [], help: "JSON 对象，例如 {\"name\":{type,value}, \"file\":{type,value}}" },
];

const PARSER_FIELDS: FieldDef[] = [
  { name: "outputs", label: "输出参数", type: "jsonArray", default: [], help: "JSON 对象：html/json/markdown/text 的类型与默认值" },
  { name: "setups", label: "解析格式配置", type: "jsonArray", default: [], help: "JSON 数组：[{fileFormat, output_format, parse_method, preprocess, …}]" },
];

const TOKEN_CHUNKER_FIELDS: FieldDef[] = [
  { name: "chunk_token_size", label: "分块 Token 数", type: "number", default: 512, min: 1, max: 2048 },
  { name: "overlapped_percent", label: "重叠比例（%）", type: "number", default: 0, min: 0, max: 100 },
  { name: "delimiter_mode", label: "分隔模式", type: "select", default: "delimiter", options: [
    { value: "delimiter", label: "分隔符" },
    { value: "token", label: "纯 Token" },
  ] },
  { name: "delimiters", label: "分隔符列表", type: "jsonArray", default: [], help: "JSON 数组：[{\"value\":\"\\n\"}]" },
];

const TITLE_CHUNKER_FIELDS: FieldDef[] = [
  { name: "method", label: "分块方法", type: "select", default: "hierarchy", options: [
    { value: "hierarchy", label: "层级（H1…H4）" },
    { value: "group", label: "分组" },
  ] },
];

const EXTRACTOR_FIELDS: FieldDef[] = [
  { name: "extraction_rules", label: "抽取规则", type: "jsonArray", default: [], help: "JSON 数组：[{meta, rules}]" },
];

const COMPILER_FIELDS: FieldDef[] = [
  { name: "subtask_prompt", label: "编译提示词", type: "textarea", default: "" },
  { name: "template", label: "知识模板", type: "select", default: "general", options: [
    { value: "general", label: "通用模板" },
    { value: "entity", label: "实体（Entity）" },
    { value: "concept", label: "概念（Concept）" },
    { value: "topic", label: "主题（Topic）" },
  ] },
];

const LOOP_FIELDS: FieldDef[] = [
  { name: "items_list", label: "循环列表变量", type: "text", default: "" },
  { name: "to", label: "循环输出目标", type: "text", default: "" },
];

const EXCEL_FIELDS: FieldDef[] = [
  { name: "operation", label: "处理操作", type: "select", default: "read", options: [
    { value: "read", label: "读取" },
    { value: "write", label: "写入" },
  ] },
];

const SWITCH_FIELDS: FieldDef[] = [
  { name: "query", label: "查询变量", type: "text", default: "sys.query", placeholder: "{sys.query}" },
  { name: "conditions", label: "条件列表", type: "jsonArray", default: [], help: "JSON 数组：[{logical_operator, items: [{operator, value}], to: [nodeId]}]" },
];

const MESSAGE_FIELDS: FieldDef[] = [
  { name: "content", label: "消息内容", type: "jsonArray", default: [""], help: "JSON 数组，每项为一行文本；支持 {variable} 插值" },
];

const REWRITE_QUESTION_FIELDS: FieldDef[] = [
  ...LLM_FIELDS.slice(0, 8), // llm_id through max_tokens
  { name: "language", label: "目标语言", type: "text", default: "" },
  { name: "message_history_window_size", label: "历史窗口", type: "number", default: 6, min: 0, max: 50 },
];

const ITERATION_FIELDS: FieldDef[] = [
  { name: "items_ref", label: "循环列表变量", type: "text", default: "", placeholder: "{上游节点.outputs.list}" },
  { name: "maximum_loop_count", label: "最大循环次数", type: "number", default: 10, min: 1, max: 100 },
];

const WAITING_DIALOGUE_FIELDS: FieldDef[] = [];

const NOTE_FIELDS: FieldDef[] = [
  { name: "text", label: "便签内容", type: "textarea", default: "" },
];

const AGENT_FIELDS: FieldDef[] = [
  ...LLM_FIELDS.slice(0, 8),
  { name: "description", label: "描述", type: "textarea", default: "" },
  { name: "user_prompt", label: "用户提示词", type: "textarea", default: "" },
  { name: "sys_prompt", label: "系统提示词", type: "textarea", default: "" },
  { name: "max_rounds", label: "最大轮次", type: "number", default: 1, min: 1, max: 10 },
  { name: "tools", label: "工具列表", type: "jsonArray", default: [], help: "JSON 数组：工具名称列表" },
];

export const OPERATOR_FORMS: Record<string, OperatorFormDef> = {
  Begin: { label: "Begin（开始）", fields: BEGIN_FIELDS },
  Generate: { label: "Generate（大模型）", fields: LLM_FIELDS },
  LLM: { label: "LLM（大模型）", fields: LLM_FIELDS },
  Knowledge: { label: "Knowledge（知识库检索）", fields: KNOWLEDGE_FIELDS },
  Retrieval: { label: "Retrieval（检索）", fields: KNOWLEDGE_FIELDS },
  Answer: { label: "Answer（回答）", fields: [] },
  Chunker: { label: "Chunker（分块器）", fields: CHUNKER_FIELDS },
  Tool: { label: "Tool（工具）", fields: TOOL_FIELDS },
  Code: { label: "Code（代码）", fields: CODE_FIELDS },
  Categorize: { label: "Categorize（分类）", fields: CATEGORIZE_FIELDS },
  File: { label: "File（文件输入）", fields: FILE_FIELDS },
  Parser: { label: "Parser（解析）", fields: PARSER_FIELDS },
  Tokenizer: { label: "Tokenizer（分块/检索入口）", fields: TOKEN_CHUNKER_FIELDS },
  TokenChunker: { label: "TokenChunker（Token 分块）", fields: TOKEN_CHUNKER_FIELDS },
  TitleChunker: { label: "TitleChunker（标题分块）", fields: TITLE_CHUNKER_FIELDS },
  Extractor: { label: "Extractor（信息抽取）", fields: EXTRACTOR_FIELDS },
  Compiler: { label: "Compiler（知识编译）", fields: COMPILER_FIELDS },
  Loop: { label: "Loop（循环）", fields: LOOP_FIELDS },
  ExcelProcessor: { label: "ExcelProcessor（表格处理）", fields: EXCEL_FIELDS },
  Switch: { label: "Switch（条件路由）", fields: SWITCH_FIELDS },
  Message: { label: "Message（消息输出）", fields: MESSAGE_FIELDS },
  RewriteQuestion: { label: "RewriteQuestion（问题改写）", fields: REWRITE_QUESTION_FIELDS },
  Iteration: { label: "Iteration（循环）", fields: ITERATION_FIELDS },
  WaitingDialogue: { label: "WaitingDialogue（等待输入）", fields: WAITING_DIALOGUE_FIELDS },
  Note: { label: "Note（便签）", fields: NOTE_FIELDS },
  Agent: { label: "Agent（子 Agent）", fields: AGENT_FIELDS },
};

export function operatorFormFor(label?: string): OperatorFormDef | undefined {
  if (!label) return undefined;
  for (const k of Object.keys(OPERATOR_FORMS)) {
    if (label === k || label.includes(k)) return OPERATOR_FORMS[k];
  }
  return undefined;
}

export function defaultParams(label: string): Record<string, any> {
  const def = operatorFormFor(label);
  const out: Record<string, any> = {};
  for (const f of def?.fields ?? []) {
    if (Array.isArray(f.default)) {
      // use a serializable seed only for explicit defaults; jsonArray empty => []
    }
    if (f.default !== undefined && f.type !== "jsonArray") out[f.name] = f.default;
  }
  return out;
}


