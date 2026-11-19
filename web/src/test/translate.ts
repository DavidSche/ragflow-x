// A translate stub that reads the real dictionary.
//
// Component tests used to mock `useTranslate` as `(key) => key`, which made an
// assertion like `getByText("reportCenter.label_revision.published")` pass while
// proving only that the component asked for a key — not that the key exists, and
// not what the reader would see. Now that user-visible enum words live in
// `zh.ts`/`en.ts`, a test can resolve keys against the shipped dictionary: the
// assertion stays human-readable Chinese *and* fails when a key is missing or its
// wording changes.
import en from "../i18n/en";
import zh from "../i18n/zh";

type Dictionary = Record<string, unknown>;

function lookup(dictionary: Dictionary, key: string): string | undefined {
  let node: unknown = dictionary;
  for (const part of key.split(".")) {
    if (typeof node !== "object" || node === null) return undefined;
    node = (node as Dictionary)[part];
  }
  return typeof node === "string" ? node : undefined;
}

/** `%{name}` / `%{ count }` placeholders, the only interpolation syntax react-admin honours. */
function interpolate(message: string, options?: Record<string, unknown>): string {
  if (!options) return message;
  return message.replace(/%{\s*([\w.]+)\s*}/g, (match, name: string) =>
    name in options ? String(options[name]) : match,
  );
}

/**
 * Builds a `t` for tests. Unknown keys answer their `_` default (react-admin's own
 * contract), else the key itself, so a missing translation is visible in the
 * failure output instead of silently rendering nothing.
 */
export function translateFor(locale: "zh" | "en" = "zh") {
  const dictionary = (locale === "en" ? en : zh) as unknown as Dictionary;
  return (key: string, options?: Record<string, unknown>): string => {
    const message = lookup(dictionary, key);
    if (message === undefined) return String(options?._ ?? key);
    return interpolate(message, options);
  };
}

/** Convenience: the zh translator, the default assertion language. */
export const tZh = translateFor("zh");
