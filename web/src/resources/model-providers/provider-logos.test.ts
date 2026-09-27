import { describe, expect, it } from "vitest";

import { getProviderLogo, sortProviders } from "./provider-logos";

describe("sortProviders", () => {
  it("places the OpenAI compatible provider first for private deployments", () => {
    expect(
      sortProviders([
        { name: "Azure-OpenAI" },
        { name: "Unknown Provider" },
        { name: "Ollama" },
        { name: "OpenAI-API-Compatible" },
      ]).map((provider) => provider.name),
    ).toEqual([
      "OpenAI-API-Compatible",
      "Ollama",
      "Azure-OpenAI",
      "Unknown Provider",
    ]);
  });

  it("keeps unlisted providers ordered by name after the preferred list", () => {
    expect(
      sortProviders([{ name: "Local AI" }, { name: "Custom" }, { name: "Custom" }]),
    ).toEqual([{ name: "Custom" }, { name: "Custom" }, { name: "Local AI" }]);
  });
});

describe("getProviderLogo", () => {
  it("uses a stable generic mark for catalog providers without a brand SVG", () => {
    const logo = getProviderLogo("Moonshot");

    expect(logo.tag).toBe("Moonshot");
    expect(logo.color).toContain("text-");
  });
});
