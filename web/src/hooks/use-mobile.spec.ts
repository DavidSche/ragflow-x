import { act, renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";
import { useIsMobile } from "./use-mobile";

// jsdom has no layout engine, so matchMedia + innerWidth drive the hook.
function setViewport(width: number, matches: boolean): void {
  Object.defineProperty(window, "innerWidth", {
    writable: true,
    configurable: true,
    value: width,
  });
  const mm = window.matchMedia as unknown as ReturnType<typeof vi.fn>;
  mm.mockImplementation((query: string) => ({
    matches,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
}

describe("useIsMobile", () => {
  test("returns false on a desktop viewport", async () => {
    setViewport(1920, false);
    const { result } = renderHook(() => useIsMobile());
    await act(async () => {});
    expect(result.current).toBe(false);
  });

  test("returns true on a mobile viewport", async () => {
    setViewport(414, true);
    const { result } = renderHook(() => useIsMobile());
    await act(async () => {});
    expect(result.current).toBe(true);
  });
});
