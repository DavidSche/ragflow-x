import { describe, expect, it } from "vitest";
import { resolveEmbeddingModelId } from "./embeddingModel";

const models = [
  { model_id: "1777b7caa83011f1a1127f02938890be", name: "qwen3-embedding-0.6b" },
  { model_id: "b7ca1777a83011f1a1127f02938890be", name: "text-embedding-3-small" },
];

describe("resolveEmbeddingModelId", () => {
  it("resolves exact tenant model ids and human-readable names", () => {
    expect(resolveEmbeddingModelId(models, "1777b7caa83011f1a1127f02938890be"))
      .toBe("1777b7caa83011f1a1127f02938890be");
    expect(resolveEmbeddingModelId(models, "text-embedding-3-small"))
      .toBe("b7ca1777a83011f1a1127f02938890be");
  });

  it("maps legacy composite identifiers by their base model name", () => {
    expect(
      resolveEmbeddingModelId(
        models,
        "qwen3-embedding-0.6b@deepseek-v4-flash-0731@OpenAI-API-Compatible",
      ),
    ).toBe("1777b7caa83011f1a1127f02938890be");
  });

  it("does not guess when multiple deployments share a base model name", () => {
    const ambiguous = [
      ...models,
      { model_id: "02938890bea83011f1a1127f02938890be", name: "qwen3-embedding-0.6b" },
    ];
    expect(resolveEmbeddingModelId(
      ambiguous,
      "qwen3-embedding-0.6b@deepseek-v4-flash-0731@OpenAI-API-Compatible",
    )).toBe("");
  });
});
