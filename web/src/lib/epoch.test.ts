import { describe, expect, it } from "vitest";
import { epochSeconds, formatEpoch } from "./epoch";

describe("epoch unit detection", () => {
  it("keeps second-precision values as they are", () => {
    expect(epochSeconds(1795048252)).toBe(1795048252);
  });

  it("reads the engine's millisecond values as milliseconds", () => {
    // GET /agents/{id}/versions returns this exact shape. Treating it as
    // seconds is what produced "58852/10/24" in the version tab.
    const seconds = epochSeconds(1795048252290);
    expect(seconds).toBe(1795048252.29);
    const year = new Date((seconds as number) * 1000).getFullYear();
    expect(year).toBeGreaterThan(2000);
    expect(year).toBeLessThan(2100);
  });

  it("handles microsecond and nanosecond values", () => {
    expect(epochSeconds(1795048252290000)).toBeCloseTo(1795048252.29, 0);
    expect(epochSeconds(1795048252290000000)).toBeCloseTo(1795048252.29, 0);
  });

  it("rejects values that are not timestamps", () => {
    expect(epochSeconds(0)).toBeNull();
    expect(epochSeconds(-1)).toBeNull();
    expect(epochSeconds(undefined)).toBeNull();
    expect(epochSeconds(null)).toBeNull();
    expect(epochSeconds(Number.NaN)).toBeNull();
    expect(formatEpoch(0)).toBeNull();
    expect(formatEpoch(undefined)).toBeNull();
  });

  it("formats a millisecond timestamp instead of printing an id", () => {
    const text = formatEpoch(1795048252290);
    expect(text).toBeTruthy();
    expect(text).not.toContain("1795");
    expect(text).toMatch(/\d{4}/);
  });
});
