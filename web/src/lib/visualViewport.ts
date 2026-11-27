import { useEffect } from "react";

/**
 * doc/314 第四节末：手机上"看得见的区域"和"布局所在的高度"是两个数。
 *
 * iOS Safari 弹出软键盘时**不动**布局视口：`window.innerHeight` 不变，`100vh`
 * 也不变，于是 `h-svh` 的应用壳仍然按键盘没弹起的高度铺开，输入框被键盘盖住。
 * 真正会变的是 `window.visualViewport.height`。`interactive-widget=resizes-content`
 * （index.html）只对 Chromium 有效，Safari 不认这个 key。
 *
 * 所以这里把 visualViewport 的读数写成两个 CSS 变量：
 *
 *   --rgx-viewport-height   看得见的高度（px）
 *   --rgx-keyboard-inset    被软键盘占掉的像素数
 *
 * 应用壳用 `height: var(--rgx-viewport-height, 100dvh)`，键盘一弹壳就缩，输入框
 * 自动留在键盘上方；`env()` 那套安全区补白只处理刘海，不处理键盘。
 *
 * 为什么不放在某个组件的 effect 里：布局视口只有一个，订阅也就只该有一份。
 * `Layout` 每次挂载调用一次，卸载时解绑。
 */

/** `window.visualViewport` 里我们真正用到的两个字段（jsdom 没有这个对象）。 */
export interface VisualViewportLike {
  height: number;
  offsetTop: number;
}

/** 订阅需要的宿主能力。浏览器里就是 `window`，测试里是一个假对象。 */
export interface ViewportHost {
  innerHeight: number;
  visualViewport?: (VisualViewportLike & EventTarget) | null;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}

export interface ViewportMetrics {
  /** 看得见的高度，落进 `--rgx-viewport-height`。 */
  height: number;
  /** 被软键盘挡掉的像素数，落进 `--rgx-keyboard-inset`。 */
  keyboardInset: number;
}

export const VIEWPORT_HEIGHT_VAR = "--rgx-viewport-height";
export const KEYBOARD_INSET_VAR = "--rgx-keyboard-inset";

/**
 * 从两个读数算出一组指标。
 *
 * 没有 `visualViewport`（老桌面 Safari、jsdom）时退回 `innerHeight`：没键盘时
 * 两者是同一个数。被挡掉的部分不会是负数：`offsetTop > 0` 的情况是浏览器为了
 * 露出聚焦的输入框自己把页面推了上去，可见区域从 `offsetTop` 开始，键盘占的是
 * 剩下那一段，算出来可能为负，而负的"被挡掉"没有意义。
 */
export function readViewportMetrics(
  viewport: VisualViewportLike | null | undefined,
  innerHeight: number,
): ViewportMetrics {
  const height = viewport && viewport.height > 0 ? viewport.height : innerHeight;
  const offsetTop = viewport && viewport.offsetTop > 0 ? viewport.offsetTop : 0;
  return {
    height,
    keyboardInset: Math.max(0, innerHeight - height - offsetTop),
  };
}

/** 把指标写进根元素的行内样式。抽出来是为了能在 jsdom 里直接断言。 */
export function applyViewportMetrics(root: HTMLElement, metrics: ViewportMetrics): void {
  root.style.setProperty(VIEWPORT_HEIGHT_VAR, `${Math.round(metrics.height)}px`);
  root.style.setProperty(KEYBOARD_INSET_VAR, `${Math.round(metrics.keyboardInset)}px`);
}

/**
 * 订阅一次视觉视口的变化，并立刻写一次。返回解绑函数。
 *
 * `scroll` 也要听：iOS 在键盘弹起时会把页面往上推（`offsetTop` 变），只听到
 * `resize` 会留下"高度对了、位置没对"的状态。
 */
export function subscribeViewportMetrics(root: HTMLElement, host: ViewportHost): () => void {
  const viewport = host.visualViewport ?? null;
  const sync = () => {
    // `innerHeight` 在 iOS 上不随键盘变化，这正是它作为被减数的原因。
    applyViewportMetrics(root, readViewportMetrics(viewport, host.innerHeight));
  };
  sync();
  viewport?.addEventListener("resize", sync);
  viewport?.addEventListener("scroll", sync);
  // 桌面浏览器的窗口缩放走这里；有 visualViewport 时它也会跟着 resize，重复写
  // 同一个值，无害。
  host.addEventListener("resize", sync);
  return () => {
    viewport?.removeEventListener("resize", sync);
    viewport?.removeEventListener("scroll", sync);
    host.removeEventListener("resize", sync);
  };
}

/** 在应用壳上订阅一次视觉视口。 */
export function useVisualViewportHeight(): void {
  useEffect(() => {
    if (typeof window === "undefined" || typeof document === "undefined") {
      return;
    }
    // `window` 的 `addEventListener` 是带事件名映射的重载，结构上和 ViewportHost
    // 的窄签名不相等，运行时完全是同一件事。
    const host = window as unknown as ViewportHost;
    if (typeof host.addEventListener !== "function") {
      return;
    }
    return subscribeViewportMetrics(document.documentElement, host);
  }, []);
}
