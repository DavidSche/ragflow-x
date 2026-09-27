import { describe, expect, it } from "vitest";
import { clampInteger, clampNumber, parseBoundedInteger } from "./numeric";

describe("numeric form helpers", () => {
  it("clamps decimals and integers to backend-safe ranges", () => {
    expect(clampNumber(1.2, 0, 1)).toBe(1);
    expect(clampNumber(-2, 0, 100, 20)).toBe(0);
    expect(clampInteger(12.6, 1, 10)).toBe(10);
  });

  it("uses a fallback for invalid numbers", () => {
    expect(parseBoundedInteger("not-a-number", 16, 2048, 512)).toBe(512);
  });
});
