import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";

export function workbenchRedirectSearchParams(searchParams: URLSearchParams): URLSearchParams {
  const targetId = searchParams.get("chat") ?? searchParams.get("chatId");
  const contextId = searchParams.get("sessionId");
  const nextParams = new URLSearchParams();
  nextParams.set("kind", "chat");
  if (targetId) nextParams.set("targetId", targetId);
  if (targetId && contextId) nextParams.set("contextId", contextId);
  return nextParams;
}

export function useWorkbenchCompat(): URLSearchParams {
  const [searchParams] = useSearchParams();
  return useMemo(() => workbenchRedirectSearchParams(searchParams), [searchParams]);
}
