export type FieldType =
  | "text"
  | "textarea"
  | "number"
  | "slider"
  | "select"
  | "switch"
  | "jsonArray"
  /** Dropdown over the tenant model catalog (value = engine `name@provider`). */
  | "modelSelect"
  /** Checkbox list over a pickable resource (value = engine id array). */
  | "resourceMulti";

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
  /** `modelSelect`: which catalog slice to offer. */
  modelKind?: "chat" | "rerank" | "image2text";
  /** `resourceMulti`: which picker list to offer. */
  resource?: "dataset" | "memory";
  /** `modelSelect`: the engine refuses to run the node without a value. */
  required?: boolean;
  /** Only show the field when another form value is one of `equals`. */
  visibleWhen?: { field: string; equals: string[] };
}

export interface OperatorFormDef {
  label: string;
  fields: FieldDef[];
}

const LLM_FIELDS: FieldDef[] = [
  { name: "llm_id", label: "agents.of_llm_id", type: "modelSelect", modelKind: "chat", required: true, default: "" },
  { name: "parameter", label: "agents.of_parameter", type: "select", default: "Custom", options: [
    { value: "creativity", label: "agents.of_opt_creativity" },
    { value: "precision", label: "agents.of_opt_precision" },
    { value: "balance", label: "agents.of_opt_balance" },
    { value: "Custom", label: "agents.of_opt_custom" },
  ] },
  { name: "temperature", label: "agents.of_temperature", type: "slider", default: 0.1, min: 0, max: 1, step: 0.01 },
  { name: "top_p", label: "Top P", type: "slider", default: 0.3, min: 0, max: 1, step: 0.01 },
  { name: "presence_penalty", label: "agents.of_presence_penalty", type: "slider", default: 0.4, min: -2, max: 2, step: 0.1 },
  { name: "frequency_penalty", label: "agents.of_frequency_penalty", type: "slider", default: 0.7, min: -2, max: 2, step: 0.1 },
  { name: "max_tokens", label: "agents.of_max_tokens", type: "number", default: 256, min: 16, max: 4096 },
  { name: "thinking", label: "agents.of_thinking", type: "select", default: "default", options: [
    { value: "default", label: "agents.of_opt_default" },
    { value: "enabled", label: "agents.of_opt_enabled" },
    { value: "disabled", label: "agents.of_opt_disabled" },
  ] },
  { name: "system", label: "agents.of_system", type: "textarea", default: "" },
  { name: "prompt", label: "agents.of_prompt", type: "textarea", default: "" },
];

const KNOWLEDGE_FIELDS: FieldDef[] = [
  { name: "retrieval_from", label: "agents.of_retrieval_from", type: "select", default: "dataset", options: [
    { value: "dataset", label: "agents.of_opt_dataset" },
    { value: "memory", label: "agents.of_opt_memory" },
  ] },
  { name: "kb_ids", label: "agents.of_kb_ids", type: "resourceMulti", resource: "dataset", default: [], help: "agents.of_kb_ids_help", visibleWhen: { field: "retrieval_from", equals: ["dataset"] } },
  { name: "memory_ids", label: "agents.of_memory_ids", type: "resourceMulti", resource: "memory", default: [], help: "agents.of_memory_ids_help", visibleWhen: { field: "retrieval_from", equals: ["memory"] } },
  { name: "query", label: "agents.of_query", type: "text", default: "{sys.query}", placeholder: "{sys.query}" },
  { name: "top_n", label: "Top N", type: "number", default: 8, min: 1, max: 20 },
  { name: "top_k", label: "Top K", type: "number", default: 1024, min: 1, max: 4096 },
  { name: "similarity_threshold", label: "agents.of_similarity_threshold", type: "slider", default: 0.2, min: 0, max: 1, step: 0.01 },
  { name: "keywords_similarity_weight", label: "agents.of_keywords_similarity_weight", type: "slider", default: 0.7, min: 0, max: 1, step: 0.01 },
  { name: "vector_similarity_weight", label: "agents.of_vector_similarity_weight", type: "slider", default: 0.3, min: 0, max: 1, step: 0.01 },
  { name: "rerank_id", label: "agents.of_rerank_id", type: "modelSelect", modelKind: "rerank", default: "", help: "agents.of_rerank_id_help", visibleWhen: { field: "retrieval_from", equals: ["dataset"] } },
  { name: "cross_languages", label: "agents.of_cross_languages", type: "jsonArray", default: [], help: "agents.of_cross_languages_help" },
  { name: "empty_response", label: "agents.of_empty_response", type: "textarea", default: "" },
  { name: "outputs", label: "agents.of_outputs", type: "jsonArray", default: {}, help: "agents.of_outputs_help" },
];

const BEGIN_FIELDS: FieldDef[] = [
  { name: "mode", label: "agents.of_mode", type: "select", default: "conversational", options: [
    { value: "conversational", label: "agents.of_opt_conversational" },
    { value: "task", label: "agents.of_opt_task" },
    { value: "webhook", label: "Webhook" },
  ] },
  { name: "enablePrologue", label: "agents.of_enableprologue", type: "switch", default: false },
  { name: "prologue", label: "agents.of_prologue", type: "textarea", default: "" },
  { name: "layout_recognize", label: "agents.of_layout_recognize", type: "select", default: "", options: [
    { value: "", label: "agents.of_opt_not_enabled" },
    { value: "default", label: "agents.of_opt_default" },
  ] },
  { name: "inputs", label: "agents.of_inputs", type: "jsonArray", default: [], help: "agents.of_inputs_help" },
];

const CHUNKER_FIELDS: FieldDef[] = [
  { name: "chunk_method", label: "agents.of_chunk_method", type: "select", default: "naive", options: [
    { value: "naive", label: "agents.of_opt_naive" },
    { value: "fqa", label: "agents.of_opt_fqa" },
    { value: "paper", label: "agents.of_opt_paper" },
    { value: "book", label: "agents.of_opt_book" },
    { value: "laws", label: "agents.of_opt_laws" },
    { value: "manual", label: "agents.of_opt_manual" },
    { value: "table", label: "agents.of_opt_table" },
    { value: "one", label: "agents.of_opt_one" },
    { value: "email", label: "agents.of_opt_email" },
  ] },
  { name: "chunk_token_num", label: "agents.of_chunk_token_num", type: "number", default: 128, min: 1, max: 1024 },
  { name: "overlap", label: "agents.of_overlap", type: "number", default: 16, min: 0, max: 100 },
  { name: "delimiter", label: "agents.of_delimiter", type: "textarea", default: "\\n!?;。；！？" },
  { name: "html4excel", label: "agents.of_html4excel", type: "switch", default: false },
];

const TOOL_FIELDS: FieldDef[] = [
  { name: "tools", label: "agents.of_tools", type: "jsonArray", default: [], help: "agents.of_tools_help" },
  { name: "server_url", label: "agents.of_server_url", type: "text", default: "" },
  { name: "method", label: "agents.of_method", type: "select", default: "GET", options: ["GET", "POST", "PUT", "DELETE"].map((m) => ({ value: m, label: m })) },
  { name: "headers", label: "agents.of_headers", type: "jsonArray", default: {}, help: "agents.of_headers_help" },
];

const CODE_FIELDS: FieldDef[] = [
  { name: "language", label: "agents.of_language", type: "select", default: "python", options: [{ value: "python", label: "Python" }] },
  { name: "code", label: "agents.of_code", type: "textarea", default: "" },
  { name: "timeout", label: "agents.of_timeout", type: "number", default: 60, min: 1, max: 600 },
  { name: "max_tokens", label: "agents.of_max_tokens", type: "number", default: 512, min: 16, max: 8192 },
];

const CATEGORIZE_FIELDS: FieldDef[] = [
  { name: "query", label: "agents.of_query", type: "text", default: "{sys.query}", placeholder: "{sys.query}" },
  { name: "llm_id", label: "agents.of_llm_id", type: "modelSelect", modelKind: "chat", required: true, default: "" },
  { name: "parameter", label: "agents.of_parameter", type: "select", default: "Custom", options: [
    { value: "creativity", label: "agents.of_opt_creativity" },
    { value: "precision", label: "agents.of_opt_precision" },
    { value: "balance", label: "agents.of_opt_balance" },
    { value: "Custom", label: "agents.of_opt_custom" },
  ] },
  { name: "temperature", label: "agents.of_temperature", type: "slider", default: 0.1, min: 0, max: 1, step: 0.01 },
  { name: "message_history_window_size", label: "agents.of_message_history_window_size", type: "number", default: 1, min: 0, max: 50 },
  { name: "description", label: "agents.of_description", type: "textarea", default: "" },
  { name: "items", label: "agents.of_items", type: "jsonArray", default: [], help: "agents.of_items_help" },
  { name: "outputs", label: "agents.of_outputs", type: "jsonArray", default: {}, help: "agents.of_outputs_help_2" },
];

// ─── Data-pipeline operators (dataflow_canvas) ─────────────────────────

const FILE_FIELDS: FieldDef[] = [
  { name: "outputs", label: "agents.of_outputs_2", type: "jsonArray", default: [], help: "agents.of_outputs_help_3" },
];

const PARSER_FIELDS: FieldDef[] = [
  { name: "outputs", label: "agents.of_outputs_2", type: "jsonArray", default: [], help: "agents.of_outputs_help_4" },
  { name: "enable_vision_enhancement", label: "agents.of_vision_enhancement", type: "switch", default: false, help: "agents.of_vision_enhancement_help" },
  { name: "vlm", label: "agents.of_vision_model", type: "modelSelect", modelKind: "image2text", default: {}, help: "agents.of_vision_model_help" },
  { name: "setups", label: "agents.of_setups", type: "jsonArray", default: [], help: "agents.of_setups_help" },
];

const TOKEN_CHUNKER_FIELDS: FieldDef[] = [
  { name: "chunk_token_size", label: "agents.of_chunk_token_num", type: "number", default: 512, min: 1, max: 2048 },
  { name: "overlapped_percent", label: "agents.of_overlapped_percent", type: "number", default: 0, min: 0, max: 100 },
  { name: "delimiter_mode", label: "agents.of_delimiter_mode", type: "select", default: "delimiter", options: [
    { value: "delimiter", label: "agents.of_delimiter" },
    { value: "token", label: "agents.of_opt_token" },
  ] },
  { name: "delimiters", label: "agents.of_delimiters", type: "jsonArray", default: [], help: "agents.of_delimiters_help" },
];

// The Tokenizer is the vectorisation stage, not a chunker: the engine requires
// `search_method` (`schema.TokenizerParam.Validate`) and reads `fields` +
// `filename_embd_weight`. Sharing the chunker field list is how a palette-added
// Tokenizer ended up with no retrieval mode at all.
const TOKENIZER_FIELDS: FieldDef[] = [
  { name: "search_method", label: "agents.of_search_method", type: "jsonArray", default: ["embedding", "full_text"], help: "agents.of_search_method_help" },
  { name: "fields", label: "agents.of_index_field", type: "select", default: "text", options: [
    { value: "text", label: "agents.of_opt_body" },
    { value: "questions", label: "agents.of_opt_questions" },
    { value: "summary", label: "agents.of_opt_summary" },
  ] },
  { name: "filename_embd_weight", label: "agents.of_filename_weight", type: "number", default: 0.1, min: 0, max: 1 },
];

const TITLE_CHUNKER_FIELDS: FieldDef[] = [
  { name: "method", label: "agents.of_chunk_method", type: "select", default: "hierarchy", options: [
    { value: "hierarchy", label: "agents.of_opt_hierarchy" },
    { value: "group", label: "agents.of_opt_group" },
  ] },
];

const EXTRACTOR_FIELDS: FieldDef[] = [
  { name: "llm_id", label: "agents.of_llm_id", type: "modelSelect", modelKind: "chat", default: "", help: "agents.of_extractor_llm_help" },
  { name: "keywords", label: "agents.of_extract_keywords", type: "jsonArray", default: { enabled: false, top_n: 3 }, help: "agents.of_extract_group_help" },
  { name: "questions", label: "agents.of_extract_questions", type: "jsonArray", default: { enabled: false, top_n: 3 }, help: "agents.of_extract_group_help" },
  { name: "tags", label: "agents.of_extract_tags", type: "jsonArray", default: { enabled: false, top_n: 3 }, help: "agents.of_extract_group_help" },
];

const COMPILER_FIELDS: FieldDef[] = [
  { name: "compilation_template_id", label: "agents.of_compilation_template", type: "text", default: "", help: "agents.of_compilation_template_help" },
  { name: "compilation_template_group_id", label: "agents.of_compilation_template_group", type: "text", default: "", help: "agents.of_compilation_template_group_help" },
  { name: "subtask_prompt", label: "agents.of_subtask_prompt", type: "textarea", default: "" },
];

const LOOP_FIELDS: FieldDef[] = [
  { name: "items_list", label: "agents.of_items_list", type: "text", default: "" },
  { name: "to", label: "agents.of_to", type: "text", default: "" },
];

const EXCEL_FIELDS: FieldDef[] = [
  { name: "operation", label: "agents.of_operation", type: "select", default: "read", options: [
    { value: "read", label: "agents.of_opt_read" },
    { value: "write", label: "agents.of_opt_write" },
  ] },
];

const SWITCH_FIELDS: FieldDef[] = [
  { name: "query", label: "agents.of_query", type: "text", default: "sys.query", placeholder: "{sys.query}" },
  { name: "conditions", label: "agents.of_conditions", type: "jsonArray", default: [], help: "agents.of_conditions_help" },
];

const MESSAGE_FIELDS: FieldDef[] = [
  { name: "content", label: "agents.of_content", type: "jsonArray", default: [""], help: "agents.of_content_help" },
];

const REWRITE_QUESTION_FIELDS: FieldDef[] = [
  ...LLM_FIELDS.slice(0, 8), // llm_id through max_tokens
  { name: "language", label: "agents.of_language_2", type: "text", default: "" },
  { name: "message_history_window_size", label: "agents.of_message_history_window_size", type: "number", default: 6, min: 0, max: 50 },
];

const ITERATION_FIELDS: FieldDef[] = [
  { name: "items_ref", label: "agents.of_items_list", type: "text", default: "", placeholder: "agents.of_items_ref_placeholder" },
  { name: "maximum_loop_count", label: "agents.of_maximum_loop_count", type: "number", default: 10, min: 1, max: 100 },
];

const WAITING_DIALOGUE_FIELDS: FieldDef[] = [];

const NOTE_FIELDS: FieldDef[] = [
  { name: "text", label: "agents.of_text", type: "textarea", default: "" },
];

const AGENT_FIELDS: FieldDef[] = [
  ...LLM_FIELDS.slice(0, 8),
  { name: "description", label: "agents.of_description_2", type: "textarea", default: "" },
  { name: "user_prompt", label: "agents.of_user_prompt", type: "textarea", default: "" },
  { name: "sys_prompt", label: "agents.of_system", type: "textarea", default: "" },
  { name: "max_rounds", label: "agents.of_max_rounds", type: "number", default: 1, min: 1, max: 10 },
  { name: "tools", label: "agents.of_tools", type: "jsonArray", default: [], help: "agents.of_tools_help_2" },
];

export const OPERATOR_FORMS: Record<string, OperatorFormDef> = {
  Begin: { label: "agents.of_begin_title", fields: BEGIN_FIELDS },
  Generate: { label: "agents.of_generate_title", fields: LLM_FIELDS },
  LLM: { label: "agents.of_llm_title", fields: LLM_FIELDS },
  Knowledge: { label: "agents.of_knowledge_title", fields: KNOWLEDGE_FIELDS },
  Retrieval: { label: "agents.of_retrieval_title", fields: KNOWLEDGE_FIELDS },
  Answer: { label: "agents.of_answer_title", fields: [] },
  Chunker: { label: "agents.of_chunker_title", fields: CHUNKER_FIELDS },
  Tool: { label: "agents.of_tool_title", fields: TOOL_FIELDS },
  Code: { label: "agents.of_code_title", fields: CODE_FIELDS },
  Categorize: { label: "agents.of_categorize_title", fields: CATEGORIZE_FIELDS },
  File: { label: "agents.of_file_title", fields: FILE_FIELDS },
  Parser: { label: "agents.of_parser_title", fields: PARSER_FIELDS },
  Tokenizer: { label: "agents.of_tokenizer_title", fields: TOKENIZER_FIELDS },
  TokenChunker: { label: "agents.of_tokenchunker_title", fields: TOKEN_CHUNKER_FIELDS },
  TitleChunker: { label: "agents.of_titlechunker_title", fields: TITLE_CHUNKER_FIELDS },
  Extractor: { label: "agents.of_extractor_title", fields: EXTRACTOR_FIELDS },
  Compiler: { label: "agents.of_compiler_title", fields: COMPILER_FIELDS },
  Loop: { label: "agents.of_loop_title", fields: LOOP_FIELDS },
  ExcelProcessor: { label: "agents.of_excelprocessor_title", fields: EXCEL_FIELDS },
  Switch: { label: "agents.of_switch_title", fields: SWITCH_FIELDS },
  Message: { label: "agents.of_message_title", fields: MESSAGE_FIELDS },
  RewriteQuestion: { label: "agents.of_rewritequestion_title", fields: REWRITE_QUESTION_FIELDS },
  Iteration: { label: "agents.of_iteration_title", fields: ITERATION_FIELDS },
  WaitingDialogue: { label: "agents.of_waitingdialogue_title", fields: WAITING_DIALOGUE_FIELDS },
  Note: { label: "agents.of_note_title", fields: NOTE_FIELDS },
  Agent: { label: "agents.of_agent_title", fields: AGENT_FIELDS },
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
    // Multi-value fields (jsonArray / resourceMulti) are seeded only when the
    // form definition carries a non-empty default: writing `"[]"` into a brand
    // new node would replace the engine's own default binding with an empty one.
    if (Array.isArray(f.default) && f.default.length === 0) continue;
    if (f.default !== undefined && f.type !== "jsonArray") out[f.name] = f.default;
  }
  return out;
}


