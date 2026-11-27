import { describe, expect, it } from "vitest";
import {
  applyViewportMetrics,
  KEYBOARD_INSET_VAR,
  readViewportMetrics,
  subscribeViewportMetrics,
  VIEWPORT_HEIGHT_VAR,
  type VisualViewportLike,
  type ViewportHost,
} from "./visualViewport";

/**
 * doc/314 第四节末 / doc/305 §2.73。
 *
 * 这里证明的是"读数到变量"这一段：谁把 `visualViewport.height` 算成壳的高度、
 * 什么时候重算、解绑之后还会不会重算。**不**证明软键盘真的把
 * `visualViewport.height` 变小——那是浏览器的行为，jsdom 里连
 * `window.visualViewport` 都没有。浏览器里那一半由
 * `web/e2e/chat-only-first-screen.spec.ts` 的 ⑤ 读，它也只能读到变量有没有被写上、
 * 壳有没有按它定高，读不到键盘。
 */
class FakeViewport implements VisualViewportLike {
  private listeners = new Map<string, Set<() => void>>();

  constructor(
    public height: number,
    public offsetTop = 0,
  ) {}

  addEventListener(type: string, listener: () => void): void {
    const set = this.listeners.get(type) ?? new Set();
    set.add(listener);
    this.listeners.set(type, set);
  }

  removeEventListener(type: string, listener: () => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }

  get count(): number {
    let total = 0;
    for (const set of this.listeners.values()) total += set.size;
    return total;
  }
}

class FakeHost implements ViewportHost {
  private listeners = new Map<string, Set<() => void>>();

  constructor(
    public innerHeight: number,
    public visualViewport: (VisualViewportLike & EventTarget) | null = null,
  ) {}

  addEventListener(type: string, listener: () => void): void {
    const set = this.listeners.get(type) ?? new Set();
    set.add(listener);
    this.listeners.set(type, set);
  }

  removeEventListener(type: string, listener: () => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }

  get count(): number {
    let total = 0;
    for (const set of this.listeners.values()) total += set.size;
    return total;
  }
}

const fakeViewport = (height: number, offsetTop = 0) =>
  new FakeViewport(height, offsetTop) as unknown as VisualViewportLike & EventTarget;

describe("visual viewport metrics", () => {
  it("takes the visible height, not the layout height", () => {
    // iOS 键盘弹起：innerHeight 不动（844），看得见的只剩 500。
    expect(readViewportMetrics({ height: 500, offsetTop: 0 }, 844)).toEqual({
      height: 500,
      keyboardInset: 344,
    });
  });

  it("falls back to the layout height when the browser has no visualViewport", () => {
    expect(readViewportMetrics(null, 844)).toEqual({ height: 844, keyboardInset: 0 });
    expect(readViewportMetrics(undefined, 844)).toEqual({ height: 844, keyboardInset: 0 });
  });

  it("counts the part the page was pushed up as visible, not as keyboard", () => {
    // 浏览器为了露出输入框把页面推上去 100px：挡住的只有剩下那 244px。
    expect(readViewportMetrics({ height: 500, offsetTop: 100 }, 844)).toEqual({
      height: 500,
      keyboardInset: 244,
    });
  });

  it("never reports a negative keyboard inset", () => {
    // 不可能发生，但一个恒为负的读数会变成一条恒为真的断言，所以夹到 0。
    expect(readViewportMetrics({ height: 900, offsetTop: 100 }, 844).keyboardInset).toBe(0);
  });

  it("writes both readings as rounded pixel values", () => {
    const root = document.createElement("div");
    applyViewportMetrics(root, { height: 500.4, keyboardInset: 343.6 });
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("500px");
    expect(root.style.getPropertyValue(KEYBOARD_INSET_VAR)).toBe("344px");
  });

  it("recomputes on the viewport's own resize and scroll events", () => {
    const root = document.createElement("div");
    const viewport = fakeViewport(844);
    const host = new FakeHost(844, viewport);
    subscribeViewportMetrics(root, host);

    // 订阅的那一刻就写一次，否则首帧没有值，壳会先按兜底高度画一遍。
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("844px");

    (viewport as unknown as FakeViewport).height = 512;
    (viewport as unknown as FakeViewport).emit("resize");
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("512px");
    expect(root.style.getPropertyValue(KEYBOARD_INSET_VAR)).toBe("332px");

    // 只听到 resize 会漏掉"页面被推上去"的那一半。
    (viewport as unknown as FakeViewport).offsetTop = 32;
    (viewport as unknown as FakeViewport).emit("scroll");
    expect(root.style.getPropertyValue(KEYBOARD_INSET_VAR)).toBe("300px");
  });

  it("falls back to the window resize event when there is no visualViewport", () => {
    const root = document.createElement("div");
    const host = new FakeHost(600, null);
    subscribeViewportMetrics(root, host);
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("600px");

    host.innerHeight = 700;
    host.emit("resize");
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("700px");
  });

  it("stops writing once unsubscribed", () => {
    // 没有这条，"组件卸载了但监听还在"会和正常一样绿：屏幕上没有第二份壳，
    // 变量却还在被一个已卸载的页面改写。
    const root = document.createElement("div");
    const viewport = fakeViewport(844);
    const host = new FakeHost(844, viewport);
    const unsubscribe = subscribeViewportMetrics(root, host);
    expect(host.count + (viewport as unknown as FakeViewport).count).toBe(3);
    unsubscribe();

    expect(host.count + (viewport as unknown as FakeViewport).count).toBe(0);
    (viewport as unknown as FakeViewport).height = 300;
    (viewport as unknown as FakeViewport).emit("resize");
    expect(root.style.getPropertyValue(VIEWPORT_HEIGHT_VAR)).toBe("844px");
  });
});
