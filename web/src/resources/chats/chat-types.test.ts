import { describe, expect, it } from "vitest";
import { buildEditChatBody, EMPTY_CONFIG } from "./chat-types";

const base = { ...EMPTY_CONFIG, name: "  法规问答  ", selected: ["kb1", "kb2"] };

describe("buildEditChatBody", () => {
  it("omits blank optional fields so the backend keeps its values", () => {
    const body = buildEditChatBody(base, undefined);
    expect(body.name).toBe("法规问答");
    expect(body.dataset_ids).toEqual(["kb1", "kb2"]);
    expect(body.llm_id).toBeUndefined();
    expect(body.rerank_id).toBeUndefined();
    expect(body.system).toBeUndefined();
    expect(body.prologue).toBeUndefined();
    expect(body.empty_response).toBeUndefined();
    expect(body.top_n).toBeUndefined();
    expect(body.similarity_threshold).toBeUndefined();
    expect(body.vector_similarity_weight).toBeUndefined();
    expect(body.llm_setting).toBeUndefined();
  });

  it("coerces numeric knobs and passes the parsed llm_setting through", () => {
    const body = buildEditChatBody(
      { ...base, llmId: "m1", rerankId: "rr1", topN: "8", similarity: "0.2", vectorWeight: "0.3" },
      { temperature: 0, top_p: 0.1, max_tokens: 800 },
    );
    expect(body.llm_id).toBe("m1");
    expect(body.rerank_id).toBe("rr1");
    expect(body.top_n).toBe(8);
    expect(body.similarity_threshold).toBe(0.2);
    expect(body.vector_similarity_weight).toBe(0.3);
    expect(body.llm_setting).toEqual({ temperature: 0, top_p: 0.1, max_tokens: 800 });
  });

  it("keeps the whitespace-trimmed name surfaced to the backend", () => {
    expect(buildEditChatBody(base, undefined).name).toBe("法规问答");
  });
});
