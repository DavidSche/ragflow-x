import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RefObject } from "react";
import { useConversationScrollPinning } from "./use-conversation-scroll-pinning";
import type { UseConversationScrollPinningOptions } from "./use-conversation-scroll-pinning";

function createContainer() {
  const container = document.createElement("div");
  let scrollTop = 0;
  let scrollHeight = 1200;
  Object.defineProperty(container, "clientHeight", {
    configurable: true,
    get: () => 300,
  });
  Object.defineProperty(container, "scrollHeight", {
    configurable: true,
    get: () => scrollHeight,
    set: (value: number) => {
      scrollHeight = value;
    },
  });
  Object.defineProperty(container, "scrollTop", {
    configurable: true,
    get: () => scrollTop,
    set: (value: number) => {
      scrollTop = value;
      container.dispatchEvent(new Event("scroll"));
    },
  });
  Object.defineProperty(container, "scrollTo", {
    configurable: true,
    writable: true,
    value: (options: ScrollToOptions) => {
      if (typeof options.top === "number") container.scrollTop = options.top;
    },
  });
  return container;
}

function setup(container: HTMLElement, options = {}) {
  const pendingFrames: FrameRequestCallback[] = [];
  const raf = vi.fn((callback: FrameRequestCallback) => {
    pendingFrames.push(callback);
    return pendingFrames.length;
  });
  const cancelRaf = vi.fn((id: number) => {
    delete pendingFrames[id - 1];
  });
  const baseOptions = {
    enabled: true,
    busy: true,
    messagesVersion: 1,
    sessionKey: "session-1",
    scrollRef: { current: container } as RefObject<HTMLElement>,
    raf,
    cancelRaf,
    ...options,
  };
  const rendered = renderHook(
    (props: UseConversationScrollPinningOptions) =>
      useConversationScrollPinning(props),
    { initialProps: baseOptions },
  );
  const rerenderVersion = (version: number, overrides = {}) => {
    rendered.rerender({ ...baseOptions, messagesVersion: version, ...overrides });
  };
  return { ...rendered, rerenderVersion, pendingFrames, raf, cancelRaf };
}

function flushFrames(frames: FrameRequestCallback[], count = 1) {
  act(() => {
    for (let index = 0; index < count; index += 1) {
      const callbackIndex = frames.findIndex(Boolean);
      if (callbackIndex < 0) break;
      frames.splice(callbackIndex, 1)[0]?.(performance.now());
    }
  });
}

function scrollTopTo(element: HTMLElement, value: number) {
  act(() => {
    element.scrollTop = value;
  });
}

function wheelUp(element: HTMLElement) {
  act(() => {
    element.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
  });
}

function setElementScrollHeight(element: HTMLElement, value: number) {
  Object.defineProperty(element, "scrollHeight", {
    configurable: true,
    get: () => value,
  });
}

describe("useConversationScrollPinning", () => {
  let container: HTMLElement;

  beforeEach(() => {
    container = createContainer();
  });

  it("positions to bottom synchronously on mount without a program scroll session", () => {
    const result = setup(container);

    expect(container.scrollTop).toBe(900);
    expect(result.result.current.pinned).toBe(true);
    expect(result.result.current.userIntent).toBe("none");
  });

  it("does not initialize scrolling when the feature is disabled", () => {
    const result = setup(container, { enabled: false });

    expect(container.scrollTop).toBe(0);
    expect(result.result.current.pinned).toBe(true);
  });

  it("follows one message change with one rAF and exactly one scrollTop write", () => {
    const result = setup(container);
    flushFrames(result.pendingFrames);
    scrollTopTo(container, 900);
    result.raf.mockClear();
    result.rerenderVersion(2);

    expect(result.raf).toHaveBeenCalledTimes(1);
    flushFrames(result.pendingFrames);
    expect(container.scrollTop).toBe(900);
  });

  it("unpins on an upward wheel gesture and never writes scrollTop while streaming", () => {
    const result = setup(container);
    scrollTopTo(container, 800);
    wheelUp(container);

    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("viewingHistory");
    expect(result.cancelRaf).toHaveBeenCalled();

    result.raf.mockClear();
    result.cancelRaf.mockClear();
    result.rerenderVersion(3);
    expect(result.raf).not.toHaveBeenCalled();
    expect(container.scrollTop).toBe(800);
  });

  it("unpins an unattributed scrollbar drag of at least the minimum delta", () => {
    const result = setup(container);
    scrollTopTo(container, 800);
    result.rerenderVersion(2);

    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("viewingHistory");
  });

  it("accumulates sub-threshold unattributed upward scroll events before unpinning", () => {
    const result = setup(container, { threshold: 4 });
    scrollTopTo(container, 894);
    expect(result.result.current.pinned).toBe(true);

    scrollTopTo(container, 888);
    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("viewingHistory");

    result.raf.mockClear();
    result.cancelRaf.mockClear();
    result.rerenderVersion(3);
    expect(result.raf).not.toHaveBeenCalled();
    expect(container.scrollTop).toBe(888);
  });

  it("does not unpin for a small layout correction and rebuilds its baseline", () => {
    const result = setup(container);
    scrollTopTo(container, 900);
    result.rerenderVersion(2);

    expect(result.result.current.pinned).toBe(true);
  });

  it("repins automatically when the user returns near the bottom", () => {
    const result = setup(container);
    scrollTopTo(container, 700);
    wheelUp(container);
    expect(result.result.current.pinned).toBe(false);

    scrollTopTo(container, 872);
    expect(result.result.current.pinned).toBe(true);
    expect(result.result.current.userIntent).toBe("none");
  });

  it("confirms pinned only after return-to-latest reaches the bottom and is stable", () => {
    const result = setup(container, { busy: false });
    scrollTopTo(container, 700);
    wheelUp(container);

    act(() => {
      result.result.current.scrollToLatest();
    });
    expect(result.result.current.userIntent).toBe("returnToLatest");
    expect(result.result.current.pinned).toBe(false);

    scrollTopTo(container, 898);
    scrollTopTo(container, 900);
    flushFrames(result.pendingFrames);
    flushFrames(result.pendingFrames);
    expect(result.result.current.pinned).toBe(true);
    expect(result.result.current.userIntent).toBe("none");
  });

  it("does not confirm pinned when a program scroll stops before its target", () => {
    const result = setup(container, { busy: false });
    scrollTopTo(container, 700);
    act(() => {
      container.scrollTo = () => {};
      result.result.current.scrollToLatest();
    });

    scrollTopTo(container, 850);
    flushFrames(result.pendingFrames);
    flushFrames(result.pendingFrames);
    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("returnToLatest");
    expect(result.result.current.programGeneration).toBe(2);

    scrollTopTo(container, 900);
    flushFrames(result.pendingFrames);
    flushFrames(result.pendingFrames);
    expect(result.result.current.pinned).toBe(true);
    expect(result.result.current.userIntent).toBe("none");
  });

  it("takes over a program scroll on reverse scrollbar movement above its start", () => {
    const result = setup(container, { busy: false });
    scrollTopTo(container, 700);
    wheelUp(container);
    act(() => {
      container.scrollTo = () => {};
      result.result.current.scrollToLatest();
    });
    const generation = result.result.current.programGeneration;

    scrollTopTo(container, 750);
    scrollTopTo(container, 740);

    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("viewingHistory");
    expect(result.result.current.programGeneration).toBeGreaterThan(generation);
  });

  it("discards stale program callbacks and scrollend after user takeover", () => {
    const result = setup(container, { busy: false });
    scrollTopTo(container, 700);
    wheelUp(container);
    act(() => {
      result.result.current.scrollToLatest();
    });
    scrollTopTo(container, 800);
    wheelUp(container);
    const staleGeneration = result.result.current.programGeneration;

    flushFrames(result.pendingFrames);
    act(() => {
      setElementScrollHeight(container, 2200);
      container.scrollTop = 900;
      container.dispatchEvent(new Event("scrollend"));
    });

    expect(result.result.current.pinned).toBe(false);
    expect(result.result.current.userIntent).toBe("viewingHistory");
    expect(result.result.current.programGeneration).toBe(staleGeneration);
  });

  it("cancels an old session anchor transaction when switching sessions", () => {
    const result = setup(container);
    scrollTopTo(container, 500);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    act(() => {
      setElementScrollHeight(container, 1600);
      container.scrollTop = 350;
    });

    result.rerenderVersion(2, { sessionKey: "session-2" });
    flushFrames(result.pendingFrames);

    expect(container.scrollTop).toBe(1300);
  });

  it("terminates a program scroll when the user takes it over and discards stale callbacks", () => {
    const result = setup(container, { busy: false });
    scrollTopTo(container, 700);
    wheelUp(container);
    act(() => {
      result.result.current.scrollToLatest();
    });
    const generation = result.result.current.programGeneration;

    scrollTopTo(container, 800);
    wheelUp(container);
    expect(result.result.current.programGeneration).toBeGreaterThan(generation);
    expect(result.result.current.pinned).toBe(false);

    scrollTopTo(container, 872);
    flushFrames(result.pendingFrames);
    flushFrames(result.pendingFrames);
    expect(result.result.current.pinned).toBe(true);
  });

  it("restores an anchor after earlier messages are prepended", () => {
    const anchor = document.createElement("div");
    anchor.setAttribute("data-message-id", "anchor-message");
    anchor.getBoundingClientRect = () =>
      ({ top: 32 } as DOMRect);
    container.getBoundingClientRect = () => ({ top: 0 } as DOMRect);
    container.append(anchor);
    scrollTopTo(container, 500);

    const result = setup(container);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    anchor.getBoundingClientRect = () => ({ top: 132 } as DOMRect);
    scrollTopTo(container, 400);
    result.rerenderVersion(2);

    expect(container.scrollTop).toBe(500);
  });

  it("restores an unknown batch without flying to the bottom", () => {
    const result = setup(container);
    flushFrames(result.pendingFrames);
    scrollTopTo(container, 500);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    act(() => {
      setElementScrollHeight(container, 1600);
      container.scrollTop = 350;
    });
    result.rerenderVersion(2);

    expect(container.scrollTop).toBe(750);
  });

  it("cancels the anchor retry immediately when layout shifts between frames", () => {
    const anchor = document.createElement("div");
    anchor.setAttribute("data-message-id", "anchor-message");
    anchor.getBoundingClientRect = () => ({ top: 32 } as DOMRect);
    container.getBoundingClientRect = () => ({ top: 0 } as DOMRect);
    container.append(anchor);
    scrollTopTo(container, 500);

    const result = setup(container);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    anchor.remove();
    scrollTopTo(container, 400);
    result.rerenderVersion(2);

    // Retry is scheduled; a layout shift (scrollHeight change) before the next
    // frame must cancel the transaction instead of re-measuring against stale
    // geometry.
    setElementScrollHeight(container, 1600);
    flushFrames(result.pendingFrames, 4);

    expect(result.pendingFrames.every((frame) => !frame)).toBe(true);
    expect(container.scrollTop).toBe(400);
  });

  it("completes the anchor retry once the anchor appears in a later frame", () => {
    const anchor = document.createElement("div");
    anchor.setAttribute("data-message-id", "anchor-message");
    anchor.getBoundingClientRect = () => ({ top: 32 } as DOMRect);
    container.getBoundingClientRect = () => ({ top: 0 } as DOMRect);
    container.append(anchor);
    scrollTopTo(container, 500);

    const result = setup(container);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    anchor.remove();
    scrollTopTo(container, 400);
    result.rerenderVersion(2);

    // The anchor rematerialises in the retry frame; the transaction must
    // re-measure it and restore the original offset instead of giving up.
    act(() => {
      container.append(anchor);
      anchor.getBoundingClientRect = () => ({ top: 132 } as DOMRect);
    });
    flushFrames(result.pendingFrames, 4);

    expect(container.scrollTop).toBe(500);
    expect(result.pendingFrames.every((frame) => !frame)).toBe(true);
  });

  it("gives up anchor restoration after three retry attempts", () => {
    const anchor = document.createElement("div");
    anchor.setAttribute("data-message-id", "anchor-message");
    anchor.getBoundingClientRect = () => ({ top: 32 } as DOMRect);
    container.getBoundingClientRect = () => ({ top: 0 } as DOMRect);
    container.append(anchor);
    scrollTopTo(container, 500);

    const result = setup(container);
    act(() => {
      result.result.current.beginLoadEarlier(2);
    });
    anchor.remove();
    scrollTopTo(container, 400);
    result.rerenderVersion(2);

    // Three scheduled retries exhaust the budget; the transaction gives up
    // without writing scrollTop and without scheduling further frames.
    flushFrames(result.pendingFrames, 6);

    expect(result.pendingFrames.every((frame) => !frame)).toBe(true);
    expect(container.scrollTop).toBe(400);
  });

  it("cancels listeners and pending work on unmount", () => {
    const result = setup(container);
    result.raf.mockClear();
    result.rerenderVersion(2);
    result.unmount();

    expect(result.cancelRaf).toHaveBeenCalled();
    const wheelSpy = vi.fn();
    container.addEventListener("wheel", wheelSpy);
    container.dispatchEvent(new WheelEvent("wheel", { deltaY: -10 }));
    expect(wheelSpy).toHaveBeenCalled();
  });
});
