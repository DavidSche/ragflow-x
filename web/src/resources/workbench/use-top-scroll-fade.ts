import { useEffect, useRef, useState, type RefObject } from "react";
import { featureFlags } from "../../lib/featureFlags";

const defaultRaf = (callback: FrameRequestCallback) =>
  window.requestAnimationFrame(callback);
const defaultCancelRaf = (id: number) => window.cancelAnimationFrame(id);

export interface UseTopScrollFadeOptions {
  enabled?: boolean;
  containerRef?: RefObject<HTMLElement | null>;
  resizeObserver?: typeof ResizeObserver;
  raf?: typeof window.requestAnimationFrame;
  cancelRaf?: typeof window.cancelAnimationFrame;
}

export function useTopScrollFade({
  enabled = featureFlags.scrollFade,
  containerRef,
  resizeObserver =
    typeof ResizeObserver === "undefined" ? undefined : ResizeObserver,
  raf = defaultRaf,
  cancelRaf = defaultCancelRaf,
}: UseTopScrollFadeOptions): boolean {
  const [visible, setVisible] = useState(false);
  const overflowRef = useRef(false);
  const awayFromTopRef = useRef(false);
  const frameRef = useRef(0);

  useEffect(() => {
    const container = containerRef?.current;
    if (!enabled || !container) {
      overflowRef.current = false;
      awayFromTopRef.current = false;
      frameRef.current = 0;
      setVisible(false);
      return;
    }

    const syncVisibility = () => {
      const nextVisible = overflowRef.current && awayFromTopRef.current;
      setVisible((previous) => (previous === nextVisible ? previous : nextVisible));
    };
    const measureOverflow = () => {
      overflowRef.current =
        container.scrollHeight - container.clientHeight > 0;
      syncVisibility();
    };

    measureOverflow();
    awayFromTopRef.current = container.scrollTop > 0;
    syncVisibility();

    const handleScroll = () => {
      if (frameRef.current) return;
      frameRef.current = raf(() => {
        frameRef.current = 0;
        awayFromTopRef.current = container.scrollTop > 0;
        syncVisibility();
      });
    };

    const observer = resizeObserver
      ? new resizeObserver(() => measureOverflow())
      : undefined;
    container.addEventListener("scroll", handleScroll, { passive: true });
    observer?.observe(container);
    if (observer && container.firstElementChild) {
      observer.observe(container.firstElementChild);
    }

    return () => {
      container.removeEventListener("scroll", handleScroll);
      observer?.disconnect();
      if (frameRef.current) {
        cancelRaf(frameRef.current);
        frameRef.current = 0;
      }
      setVisible(false);
    };
  }, [cancelRaf, containerRef, enabled, raf, resizeObserver]);

  return visible;
}
