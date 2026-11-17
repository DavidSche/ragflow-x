import { describe, expect, it } from "vitest";
import { buildEditChatBody, EMPTY_CONFIG, sessionRows } from "./chat-types";

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

// 4.151 drill 2026-11-17: `GET /chats/:id/sessions` answers a Page envelope and
// the panel stored it directly in `SessionLike[]`, crashing the whole show page
// with `sessions.filter is not a function`.
describe("sessionRows", () => {
  it("unwraps the paged envelope the handler returns", () => {
    const rows = sessionRows<{ id: string }>({
      items: [{ id: "s1" }, { id: "s2" }],
      total: 2,
      page: 1,
      page_size: 30,
    });
    expect(rows.map((r) => r.id)).toEqual(["s1", "s2"]);
  });

  it("still accepts a bare array from older paths", () => {
    expect(sessionRows<{ id: string }>([{ id: "s1" }]).length).toBe(1);
  });

  it("never returns a non-array for an unexpected payload", () => {
    for (const payload of [null, undefined, {}, "oops", 42, { items: "nope" }, { data: [] }]) {
      expect(Array.isArray(sessionRows(payload))).toBe(true);
      expect(sessionRows(payload)).toEqual([]);
    }
  });
});
