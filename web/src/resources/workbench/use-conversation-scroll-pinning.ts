import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type RefObject,
} from "react";
import { featureFlags } from "../../lib/featureFlags";

export type ScrollPinningIntent =
  | "none"
  | "viewingHistory"
  | "returnToLatest";

interface ProgramScrollSession {
  generation: number;
  startTop: number;
  lastTop: number;
  targetTop: number;
  stableTop: number | null;
  stableCount: number;
}

interface AnchorTransaction {
  id: number;
  sessionKey: string;
  scrollHeight: number;
  targetOffset: number;
  anchorId: string | null;
  attempts: number;
  retryScrollHeight?: number;
}

export interface UseConversationScrollPinningOptions {
  enabled?: boolean;
  messagesVersion: unknown;
  sessionKey: string;
  scrollRef?: RefObject<HTMLElement | null>;
  threshold?: number;
  minUpwardDelta?: number;
  raf?: typeof window.requestAnimationFrame;
  cancelRaf?: typeof window.cancelAnimationFrame;
}

export interface UseConversationScrollPinningResult {
  pinned: boolean;
  userIntent: ScrollPinningIntent;
  programGeneration: number;
  scrollToLatest: () => void;
  beginLoadEarlier: (batchCount: number) => void;
}

const defaultRaf = (callback: FrameRequestCallback) =>
  window.requestAnimationFrame(callback);
const defaultCancelRaf = (id: number) => window.cancelAnimationFrame(id);

export function useConversationScrollPinning({
  enabled = featureFlags.scrollPinning,
  messagesVersion,
  sessionKey,
  scrollRef,
  threshold = 32,
  minUpwardDelta = 8,
  raf = defaultRaf,
  cancelRaf = defaultCancelRaf,
}: UseConversationScrollPinningOptions): UseConversationScrollPinningResult {
  const [pinned, setPinnedState] = useState(true);
  const [userIntent, setUserIntentState] =
    useState<ScrollPinningIntent>("none");
  const [programGeneration, setProgramGenerationState] = useState(0);

  const pinnedRef = useRef(true);
  const userIntentRef = useRef<ScrollPinningIntent>("none");
  const baselineTopRef = useRef(0);
  const generationRef = useRef(0);
  const followFrameRef = useRef(0);
  const anchorFrameRef = useRef(0);
  const programFrameRef = useRef(0);
  const sessionRef = useRef<ProgramScrollSession | null>(null);
  const anchorRef = useRef<AnchorTransaction | null>(null);
  const lastTouchYRef = useRef<number | null>(null);

  const setPinned = useCallback((value: boolean) => {
    pinnedRef.current = value;
    setPinnedState(value);
  }, []);

  const setUserIntent = useCallback((value: ScrollPinningIntent) => {
    userIntentRef.current = value;
    setUserIntentState(value);
  }, []);

  const setProgramGeneration = useCallback((value: number) => {
    setProgramGenerationState(value);
  }, []);

  const cancelFollow = useCallback(() => {
    if (followFrameRef.current) {
      cancelRaf(followFrameRef.current);
      followFrameRef.current = 0;
    }
  }, [cancelRaf]);

  const cancelAnchor = useCallback(() => {
    if (anchorFrameRef.current) {
      cancelRaf(anchorFrameRef.current);
      anchorFrameRef.current = 0;
    }
    anchorRef.current = null;
  }, [cancelRaf]);

  const cancelProgramFrame = useCallback(() => {
    if (programFrameRef.current) {
      cancelRaf(programFrameRef.current);
      programFrameRef.current = 0;
    }
  }, [cancelRaf]);

  const invalidateProgram = useCallback(
    (incrementGeneration: boolean) => {
      sessionRef.current = null;
      cancelProgramFrame();
      if (incrementGeneration) {
        generationRef.current += 1;
        setProgramGeneration(generationRef.current);
      }
    },
    [cancelProgramFrame, setProgramGeneration],
  );

  const positionToBottom = useCallback(() => {
    const container = scrollRef?.current;
    if (!container) return;
    const target = container.scrollHeight - container.clientHeight;
    if (container.scrollTop !== target) container.scrollTop = target;
    baselineTopRef.current = target;
  }, [scrollRef]);

  const resetToBottom = useCallback(() => {
    cancelFollow();
    cancelAnchor();
    cancelProgramFrame();
    invalidateProgram(false);
    pinnedRef.current = true;
    userIntentRef.current = "none";
    setPinnedState(true);
    setUserIntentState("none");
    positionToBottom();
  }, [
    cancelAnchor,
    cancelFollow,
    cancelProgramFrame,
    invalidateProgram,
    positionToBottom,
  ]);

  const scheduleFollow = useCallback(() => {
    const container = scrollRef?.current;
    if (
      !container ||
      !pinnedRef.current ||
      sessionRef.current ||
      anchorRef.current ||
      followFrameRef.current
    ) {
      return;
    }

    followFrameRef.current = raf(() => {
      followFrameRef.current = 0;
      if (
        !pinnedRef.current ||
        sessionRef.current ||
        anchorRef.current
      ) {
        return;
      }
      const target = container.scrollHeight - container.clientHeight;
      if (container.scrollTop !== target) {
        container.scrollTop = target;
      }
    });
  }, [raf, scrollRef]);

  const scrollToLatest = useCallback(() => {
    if (!enabled) return;
    const container = scrollRef?.current;
    if (!container) return;

    cancelFollow();
    cancelAnchor();
    cancelProgramFrame();
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    sessionRef.current = {
      generation,
      startTop: container.scrollTop,
      lastTop: container.scrollTop,
      targetTop: container.scrollHeight - container.clientHeight,
      stableTop: null,
      stableCount: 0,
    };
    setProgramGeneration(generation);
    pinnedRef.current = false;
    userIntentRef.current = "returnToLatest";
    setPinnedState(false);
    setUserIntentState("returnToLatest");
    container.scrollTo({
      top: sessionRef.current.targetTop,
      behavior: "smooth",
    });
  }, [
    cancelAnchor,
    cancelFollow,
    cancelProgramFrame,
    scrollRef,
    setProgramGeneration,
  ]);

  const beginLoadEarlier = useCallback(
    (_batchCount: number) => {
      if (!enabled) return;
      const container = scrollRef?.current;
      if (!container) return;

      const containerTop = container.getBoundingClientRect().top;
      const anchor = Array.from(
        container.querySelectorAll<HTMLElement>("[data-message-id]"),
      ).find((element) => {
        const elementTop = element.getBoundingClientRect().top;
        return elementTop >= containerTop - 1 && elementTop < containerTop + 80;
      });
      const anchorId = anchor?.getAttribute("data-message-id") ?? null;
      const targetOffset = anchor
        ? anchor.getBoundingClientRect().top - containerTop
        : 0;
      const id = generationRef.current + 1000 + (anchorRef.current?.id ?? 0);

      anchorRef.current = {
        id,
        sessionKey,
        scrollHeight: container.scrollHeight,
        targetOffset,
        anchorId,
        attempts: 0,
      };
    },
    [enabled, scrollRef, sessionKey],
  );

  useLayoutEffect(() => {
    if (!enabled) return;
    resetToBottom();
  }, [enabled, resetToBottom, sessionKey]);

  useLayoutEffect(() => {
    const container = scrollRef?.current;
    const transaction = anchorRef.current;
    if (!enabled) {
      cancelAnchor();
      return;
    }
    if (!container || !transaction) return;
    if (transaction.sessionKey !== sessionKey) {
      cancelAnchor();
      return;
    }

    const complete = () => {
      anchorFrameRef.current = 0;
      if (transaction.anchorId) {
        const anchor = container.querySelector<HTMLElement>(
          `[data-message-id="${CSS.escape(transaction.anchorId)}"]`,
        );
        // Keep the transaction registered on failure so the retry frame's id
        // guard still matches and the retry budget can actually run.
        if (!anchor) return false;
        anchorRef.current = null;
        const currentOffset =
          anchor.getBoundingClientRect().top -
          container.getBoundingClientRect().top;
        container.scrollTop += currentOffset - transaction.targetOffset;
        baselineTopRef.current = container.scrollTop;
        return true;
      }

      anchorRef.current = null;
      const batchOffset = Math.max(
        0,
        container.scrollHeight - transaction.scrollHeight,
      );
      container.scrollTop += batchOffset;
      baselineTopRef.current = container.scrollTop;
      return true;
    };

    if (complete()) return;

    const scheduleRetry = () => {
      if (anchorFrameRef.current) {
        cancelRaf(anchorFrameRef.current);
        anchorFrameRef.current = 0;
      }
      transaction.retryScrollHeight = container.scrollHeight;
      transaction.attempts += 1;
      anchorFrameRef.current = raf(() => {
        if (anchorRef.current?.id !== transaction.id) return;
        anchorFrameRef.current = 0;
        if (container.scrollHeight !== transaction.retryScrollHeight) {
          cancelAnchor();
          return;
        }
        if (complete()) return;
        if (transaction.attempts >= 3) {
          cancelAnchor();
          return;
        }
        scheduleRetry();
      });
    };

    scheduleRetry();
  }, [cancelAnchor, enabled, messagesVersion, raf, scrollRef, sessionKey]);

  useEffect(() => {
    if (enabled) scheduleFollow();
  }, [enabled, messagesVersion, scheduleFollow]);

  useEffect(() => {
    const container = scrollRef?.current;
    if (!enabled || !container) return;

    const distanceToBottom = () =>
      container.scrollHeight - container.scrollTop - container.clientHeight;

    const confirmSession = (session: ProgramScrollSession) => {
      if (sessionRef.current?.generation !== session.generation) return;
      sessionRef.current = null;
      cancelProgramFrame();
      setPinned(true);
      setUserIntent("none");
      baselineTopRef.current = container.scrollTop;
    };

    const scheduleSessionConfirm = (session: ProgramScrollSession) => {
      if (programFrameRef.current) return;
      programFrameRef.current = raf(() => {
        programFrameRef.current = 0;
        if (sessionRef.current?.generation !== session.generation) return;
        if (Math.abs(container.scrollTop - session.targetTop) >= 1) {
          session.stableTop = null;
          session.stableCount = 0;
          return;
        }
        if (session.stableTop === container.scrollTop) {
          session.stableCount += 1;
        } else {
          session.stableTop = container.scrollTop;
          session.stableCount = 1;
        }
        if (session.stableCount >= 2) {
          confirmSession(session);
          return;
        }
        scheduleSessionConfirm(session);
      });
    };

    const unpin = () => {
      setPinned(false);
      setUserIntent("viewingHistory");
      cancelFollow();
      invalidateProgram(true);
      cancelAnchor();
    };

    const handleScroll = () => {
      const top = container.scrollTop;
      const session = sessionRef.current;

      if (session) {
        const reverseScrollbarMove = top < session.lastTop - 1;
        if (top < session.startTop - 1 || reverseScrollbarMove) {
          setPinned(false);
          setUserIntent("viewingHistory");
          invalidateProgram(true);
          cancelFollow();
          baselineTopRef.current = top;
          return;
        }

        if (Math.abs(top - session.targetTop) < 1) {
          scheduleSessionConfirm(session);
        } else {
          session.stableTop = null;
          session.stableCount = 0;
        }
        session.lastTop = top;
        baselineTopRef.current = top;
        return;
      }

      if (anchorRef.current) return;

      if (distanceToBottom() <= threshold) {
        if (!pinnedRef.current) {
          setPinned(true);
          setUserIntent("none");
        }
        baselineTopRef.current = top;
      } else if (top < baselineTopRef.current) {
        if (baselineTopRef.current - top >= minUpwardDelta) {
          unpin();
          baselineTopRef.current = top;
        }
      } else {
        baselineTopRef.current = top;
      }
    };

    const handleScrollEnd = () => {
      const session = sessionRef.current;
      if (!session) return;
      if (Math.abs(container.scrollTop - session.targetTop) < 1) {
        session.stableTop = container.scrollTop;
        session.stableCount = 1;
        scheduleSessionConfirm(session);
      }
    };

    const handleWheel = (event: WheelEvent) => {
      if (event.deltaY >= 0 || distanceToBottom() <= threshold) return;
      if (sessionRef.current) {
        invalidateProgram(true);
        cancelFollow();
      }
      cancelAnchor();
      setPinned(false);
      setUserIntent("viewingHistory");
    };

    const handleTouchMove = (event: TouchEvent) => {
      const touch = event.changedTouches[0];
      if (!touch) return;
      const previousY = lastTouchYRef.current;
      lastTouchYRef.current = touch.clientY;
      if (previousY === null || touch.clientY <= previousY) return;
      if (distanceToBottom() <= threshold) return;
      if (sessionRef.current) {
        invalidateProgram(true);
        cancelFollow();
      }
      cancelAnchor();
      setPinned(false);
      setUserIntent("viewingHistory");
    };

    const handleTouchEnd = () => {
      lastTouchYRef.current = null;
    };

    const handleKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      const isWithinContainer =
        target instanceof Node &&
        (target === container || container.contains(target));
      const isUpward =
        (event.key === "ArrowUp" || event.key === "PageUp") && isWithinContainer;
      if (!isUpward || distanceToBottom() <= threshold) return;
      if (sessionRef.current) {
        invalidateProgram(true);
        cancelFollow();
      }
      cancelAnchor();
      setPinned(false);
      setUserIntent("viewingHistory");
    };

    container.addEventListener("scroll", handleScroll, { passive: true });
    container.addEventListener("wheel", handleWheel, { passive: true });
    container.addEventListener("touchmove", handleTouchMove, {
      passive: true,
    });
    container.addEventListener("touchend", handleTouchEnd, { passive: true });
    container.addEventListener("keydown", handleKeyDown);
    if ("onscrollend" in container) {
      container.addEventListener("scrollend", handleScrollEnd);
    }

    const observer = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(() => scheduleFollow());
    if (observer && container.firstElementChild) {
      observer.observe(container.firstElementChild);
    }

    return () => {
      container.removeEventListener("scroll", handleScroll);
      container.removeEventListener("wheel", handleWheel);
      container.removeEventListener("touchmove", handleTouchMove);
      container.removeEventListener("touchend", handleTouchEnd);
      container.removeEventListener("keydown", handleKeyDown);
      if ("onscrollend" in container) {
        container.removeEventListener("scrollend", handleScrollEnd);
      }
      observer?.disconnect();
      cancelFollow();
      cancelAnchor();
      cancelProgramFrame();
    };
  }, [
    cancelAnchor,
    cancelFollow,
    cancelProgramFrame,
    enabled,
    invalidateProgram,
    minUpwardDelta,
    raf,
    scheduleFollow,
    scrollRef,
    setPinned,
    setUserIntent,
    threshold,
  ]);

  return {
    pinned,
    userIntent,
    programGeneration,
    scrollToLatest,
    beginLoadEarlier,
  };
}
