import type { ConversationTargetRef } from "./types";

const timestampByTarget = new WeakMap<ConversationTargetRef, number>();

export function rememberRecentTimestamp(target: ConversationTargetRef, timestamp: number): void {
  timestampByTarget.set(target, timestamp);
}

export function entryTimestamp(target: ConversationTargetRef): number {
  return timestampByTarget.get(target) ?? Date.now();
}

export function targetSubtitle(target: ConversationTargetRef, translate: (key: string) => string): string {
  const kind = translate(`conversationCenter.kind_${target.kind}`);
  const scope = target.knowledgeScope?.length
    ? target.knowledgeScope.slice(0, 2).join("、")
    : undefined;
  return [kind, scope].filter(Boolean).join(" · ");
}
