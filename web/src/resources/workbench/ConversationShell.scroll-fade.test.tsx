import { act, render, screen } from "@testing-library/react";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  type Mock,
  vi,
} from "vitest";
import { ConversationShell } from "./ConversationShell";

vi.mock("../../lib/featureFlags", () => ({
  featureFlags: {
    scrollPinning: false,
    inputAutoGrow: false,
    scrollFade: true,
  },
}));

class ScrollFadeResizeObserver {
  static instances: { callback: ResizeObserverCallback; disconnect: Mock }[] =
    [];

  readonly disconnect = vi.fn();

  constructor(private readonly callback: ResizeObserverCallback) {
    ScrollFadeResizeObserver.instances.push({
      callback,
      disconnect: this.disconnect,
    });
  }

  observe(): void {}

  unobserve(): void {}
}

describe("<ConversationShell /> top scroll fade", () => {
  let pendingFrames: FrameRequestCallback[] = [];

  beforeEach(() => {
    ScrollFadeResizeObserver.instances = [];
    pendingFrames = [];
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      pendingFrames.push(callback);
      return pendingFrames.length;
    });
    Object.defineProperty(window, "ResizeObserver", {
      configurable: true,
      writable: true,
      value: ScrollFadeResizeObserver,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders only a 24px masked top overlay when the top is scrolled away", async () => {
    const scrollRef = { current: null as HTMLDivElement | null };
    render(
      <ConversationShell
        messages={[{ role: "assistant", content: "history" }]}
        scrollRef={scrollRef}
        messagesAriaLabel="messages"
      />,
    );
    const container = scrollRef.current!;
    Object.defineProperty(container, "clientHeight", {
      configurable: true,
      value: 200,
    });
    Object.defineProperty(container, "scrollHeight", {
      configurable: true,
      value: 800,
    });

    expect(screen.queryByTestId("conversation-top-scroll-fade")).not.toBeInTheDocument();

    act(() => {
      Object.defineProperty(container, "scrollTop", {
        configurable: true,
        writable: true,
        value: 100,
      });
      container.dispatchEvent(new Event("scroll"));
    });
    act(() => {
      pendingFrames.shift()?.(performance.now());
    });
    act(() => {
      ScrollFadeResizeObserver.instances[0]?.callback([], {} as ResizeObserver);
    });

    const overlay = await screen.findByTestId("conversation-top-scroll-fade");
    expect(overlay).toHaveClass("h-6");
    expect(overlay).toHaveClass("pointer-events-none");
    expect(overlay.style.maskImage).toContain("linear-gradient");
    expect(overlay.style.maskImage).toContain("black");
    expect(overlay.style.webkitMaskImage).toContain("linear-gradient");
    expect(screen.queryByTestId("conversation-bottom-scroll-fade")).not.toBeInTheDocument();
  });
});
