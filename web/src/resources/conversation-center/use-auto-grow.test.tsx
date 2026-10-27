import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { RefObject } from "react";
import { useAutoGrow } from "./use-auto-grow";

function createTextarea() {
  const textarea = document.createElement("textarea");
  let scrollHeight = 64;
  Object.defineProperty(textarea, "scrollHeight", {
    configurable: true,
    get: () => scrollHeight,
    set: (value: number) => {
      scrollHeight = value;
    },
  });
  return textarea;
}

function setScrollHeight(textarea: HTMLTextAreaElement, value: number) {
  Object.defineProperty(textarea, "scrollHeight", {
    configurable: true,
    get: () => value,
  });
}

describe("useAutoGrow", () => {
  it("uses the CSS path without JS height writes when field-sizing is supported", () => {
    const textarea = createTextarea();
    const { result } = renderHook(() =>
      useAutoGrow({
        enabled: true,
        value: "short",
        textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
        supportsFieldSizing: true,
      }),
    );

    expect(result.current.fieldSizingSupported).toBe(true);
    expect(textarea.style.height).toBe("");
  });

  it("grows and shrinks through the JS fallback", () => {
    const textarea = createTextarea();
    const view = renderHook(() =>
      useAutoGrow({
        enabled: true,
        value: "short",
        textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
        supportsFieldSizing: false,
      }),
    );
    setScrollHeight(textarea, 140);
    view.rerender({
      enabled: true,
      value: `${"long ".repeat(20)}`,
      textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
      supportsFieldSizing: false,
    });
    expect(textarea.style.height).toBe("140px");

    setScrollHeight(textarea, 64);
    view.rerender({
      enabled: true,
      value: "",
      textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
      supportsFieldSizing: false,
    });
    expect(textarea.style.height).toBe("64px");
  });

  it("caps the JS fallback at 160px and enables internal scrolling", () => {
    const textarea = createTextarea();
    setScrollHeight(textarea, 320);
    renderHook(() =>
      useAutoGrow({
        enabled: true,
        value: `${"long ".repeat(40)}`,
        textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
        supportsFieldSizing: false,
      }),
    );

    expect(textarea.style.height).toBe("160px");
  });

  it("does not initialize measurements when disabled", () => {
    const textarea = createTextarea();
    renderHook(() =>
      useAutoGrow({
        enabled: false,
        value: "disabled",
        textareaRef: { current: textarea } as RefObject<HTMLTextAreaElement>,
        supportsFieldSizing: false,
      }),
    );

    expect(textarea.style.height).toBe("");
  });
});
