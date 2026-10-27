type FlagStorage = Pick<Storage, "getItem"> | undefined;

export function readFlag(key: string, storage?: FlagStorage): boolean {
  if (!storage) return false;
  try {
    return storage.getItem(key) === "1";
  } catch {
    return false;
  }
}

function defaultStorage(): Storage | undefined {
  return typeof window === "undefined" ? undefined : window.localStorage;
}

export const featureFlags = {
  scrollPinning: readFlag(
    "conversationCenter.scrollPinning.enabled",
    defaultStorage(),
  ),
  inputAutoGrow: readFlag(
    "conversationCenter.inputAutoGrow.enabled",
    defaultStorage(),
  ),
  scrollFade: readFlag(
    "conversationCenter.scrollFade.enabled",
    defaultStorage(),
  ),
} as const;
