import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RefObject } from "react";
import { useTopScrollFade } from "./use-top-scroll-fade";

class FadeResizeObserver {
  static instances: {
    callback: ResizeObserverCallback;
    disconnect: ReturnType<typeof vi.fn>;
    observed: Element[];
  }[] = [];

  readonly disconnect = vi.fn();
  readonly observed: Element[] = [];

  constructor(private readonly callback: ResizeObserverCallback) {
    FadeResizeObserver.instances.push({
      callback,
      disconnect: this.disconnect,
      observed: this.observed,
    });
  }

  observe(target: Element): void {
    this.observed.push(target);
  }

  unobserve(): void {}
}

function createContainer() {
  const container = document.createElement("div");
  let scrollTop = 0;
  let scrollHeight = 800;
  Object.defineProperty(container, "clientHeight", {
    configurable: true,
    get: () => 200,
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
  return container;
}

function scrollTopTo(container: HTMLElement, value: number) {
  act(() => {
    container.scrollTop = value;
  });
}

function flushFrames(frames: FrameRequestCallback[]) {
  act(() => {
    const callbackIndex = frames.findIndex(Boolean);
    if (callbackIndex >= 0) {
      frames.splice(callbackIndex, 1)[0]?.(performance.now());
    }
  });
}

function setup(container: HTMLElement, enabled = true) {
  const pendingFrames: FrameRequestCallback[] = [];
  const raf = vi.fn((callback: FrameRequestCallback) => {
    pendingFrames.push(callback);
    return pendingFrames.length;
  });
  const cancelRaf = vi.fn((id: number) => {
    delete pendingFrames[id - 1];
  });
  FadeResizeObserver.instances = [];
  const result = renderHook(
    (props: { enabled: boolean; containerRef: RefObject<HTMLElement | null> }) =>
      useTopScrollFade({
        ...props,
        resizeObserver: FadeResizeObserver as unknown as typeof ResizeObserver,
        raf,
        cancelRaf,
      }),
    {
      initialProps: {
        enabled,
        containerRef: { current: container } as RefObject<HTMLElement | null>,
      },
    },
  );
  return { ...result, pendingFrames, raf, cancelRaf };
}

describe("useTopScrollFade", () => {
  let container: HTMLElement;

  beforeEach(() => {
    container = createContainer();
  });

  it("does not register scroll or resize observers when disabled", () => {
    const result = setup(container, false);
    scrollTopTo(container, 100);

    expect(result.result.current).toBe(false);
    expect(result.raf).not.toHaveBeenCalled();
    expect(FadeResizeObserver.instances).toHaveLength(0);
  });

  it("shows top fade only while content overflows and the viewport is away from top", () => {
    const result = setup(container);

    expect(result.result.current).toBe(false);
    scrollTopTo(container, 100);
    flushFrames(result.pendingFrames);
    expect(result.result.current).toBe(true);

    scrollTopTo(container, 0);
    flushFrames(result.pendingFrames);
    expect(result.result.current).toBe(false);
  });

  it("uses ResizeObserver independently for the overflow condition", () => {
    const result = setup(container);
    scrollTopTo(container, 100);
    flushFrames(result.pendingFrames);
    expect(result.result.current).toBe(true);

    act(() => {
      Object.defineProperty(container, "scrollHeight", {
        configurable: true,
        get: () => 200,
      });
      FadeResizeObserver.instances[0]?.callback([], {} as ResizeObserver);
    });
    expect(result.result.current).toBe(false);

    act(() => {
      Object.defineProperty(container, "scrollHeight", {
        configurable: true,
        get: () => 800,
      });
      FadeResizeObserver.instances[0]?.callback([], {} as ResizeObserver);
    });
    expect(result.result.current).toBe(true);
  });

  it("observes both the scroll viewport and its content for overflow changes", () => {
    const content = document.createElement("div");
    container.append(content);
    setup(container);
    const observer = FadeResizeObserver.instances[0];

    expect(observer?.observed).toEqual([container, content]);
  });

  it("removes listeners, cancels pending frames and disconnects on unmount", () => {
    const result = setup(container);
    const observer = FadeResizeObserver.instances[0];
    scrollTopTo(container, 100);
    result.unmount();
    result.raf.mockClear();

    act(() => {
      container.dispatchEvent(new Event("scroll"));
    });

    expect(observer?.disconnect).toHaveBeenCalled();
    expect(result.cancelRaf).toHaveBeenCalled();
    expect(result.raf).not.toHaveBeenCalled();
  });
});
