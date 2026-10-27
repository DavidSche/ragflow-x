export interface EmbeddingModelOption {
  model_id: string;
  name: string;
}

export const DEFAULT_CHUNK_TOKEN_NUM = 512;

export function resolveEmbeddingModelId(
  models: readonly EmbeddingModelOption[],
  value?: string,
): string {
  const embeddingModel = value?.trim();
  if (!embeddingModel) return "";

  const exact = models.find(
    (model) => model.model_id === embeddingModel || model.name === embeddingModel,
  );
  if (exact) return exact.model_id;

  if (!embeddingModel.includes("@")) return "";
  const baseName = embeddingModel.split("@", 1)[0]?.trim();
  if (!baseName) return "";

  const matches = models.filter((model) => model.name === baseName);
  return matches.length === 1 ? matches[0].model_id : "";
}
