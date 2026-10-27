import { describe, expect, it } from "vitest";
import { readFlag } from "./featureFlags";

describe("readFlag", () => {
  it("reads only the enabled marker from injected storage", () => {
    expect(
      readFlag("flag.enabled", { getItem: () => "1" } as Pick<Storage, "getItem">),
    ).toBe(true);
    expect(
      readFlag("flag.enabled", { getItem: () => "true" } as Pick<Storage, "getItem">),
    ).toBe(false);
    expect(
      readFlag("flag.enabled", { getItem: () => null } as Pick<Storage, "getItem">),
    ).toBe(false);
    expect(readFlag("flag.enabled", undefined)).toBe(false);
  });

  it("falls back to disabled when storage access throws", () => {
    expect(
      readFlag("flag.enabled", {
        getItem: () => {
          throw new Error("storage denied");
        },
      } as Pick<Storage, "getItem">),
    ).toBe(false);
  });
});
