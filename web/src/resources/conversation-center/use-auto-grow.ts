import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";
import { featureFlags } from "../../lib/featureFlags";

const MIN_HEIGHT = 64;
const MAX_HEIGHT = 160;

function supportsFieldSizingByDefault(): boolean {
  return (
    typeof CSS !== "undefined" &&
    typeof CSS.supports === "function" &&
    CSS.supports("field-sizing", "content")
  );
}

export interface UseAutoGrowOptions {
  enabled?: boolean;
  value: string;
  textareaRef?: RefObject<HTMLTextAreaElement | null>;
  supportsFieldSizing?: boolean;
}

export interface UseAutoGrowResult {
  fieldSizingSupported: boolean;
}

export function useAutoGrow({
  enabled = featureFlags.inputAutoGrow,
  value,
  textareaRef,
  supportsFieldSizing = supportsFieldSizingByDefault(),
}: UseAutoGrowOptions): UseAutoGrowResult {
  const [fieldSizingSupported, setFieldSizingSupported] =
    useState(supportsFieldSizing);

  const updateHeight = useCallback(() => {
    const textarea = textareaRef?.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    const height = Math.min(
      Math.max(textarea.scrollHeight, MIN_HEIGHT),
      MAX_HEIGHT,
    );
    textarea.style.height = `${height}px`;
  }, [textareaRef]);

  useEffect(() => {
    setFieldSizingSupported(supportsFieldSizing);
    if (!enabled || supportsFieldSizing) return;
    updateHeight();

    const textarea = textareaRef?.current;
    const observedElement = textarea?.parentElement;
    if (!observedElement || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => updateHeight());
    observer.observe(observedElement);
    return () => observer.disconnect();
  }, [enabled, supportsFieldSizing, textareaRef, updateHeight, value]);

  return { fieldSizingSupported };
}
