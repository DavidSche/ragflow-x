import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { DatasetConfigFields } from "./DatasetConfigFields";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn().mockResolvedValue({
      data: { data: { embedding: [{ model_id: "model-id", name: "embedding" }] } },
    }),
  },
}));
vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

describe("DatasetConfigFields", () => {
  it("defaults chunk tokens to the RAGFlow parser default", async () => {
    render(
      <DatasetConfigFields
        value={{
          chunk_method: "naive",
          embedding_model: "",
          permission: "team",
          chunk_token_num: "",
        }}
        onChange={() => undefined}
      />,
    );

    await expect(screen.findByLabelText("datasets.embedding_model")).resolves.toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /datasets\.chunk_tokens/ })).toHaveValue(512);
  });

  it("uses localized permission labels", async () => {
    render(
      <DatasetConfigFields
        value={{
          chunk_method: "naive",
          embedding_model: "",
          permission: "team",
          chunk_token_num: "512",
        }}
        onChange={() => undefined}
      />,
    );

    const permission = await screen.findByLabelText("datasets.permission");
    expect(permission).toHaveDisplayValue("datasets.permission_team");
    expect(screen.getByRole("option", { name: "datasets.permission_me" })).toBeInTheDocument();
  });
});
