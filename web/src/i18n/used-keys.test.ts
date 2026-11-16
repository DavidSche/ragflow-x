/**
 * Guards the "raw key on screen" failure mode.
 *
 * `conversationCenter.empty` was consumed by ConversationCenter.tsx but defined
 * in neither locale, so the conversation center rendered the literal string
 * `conversationCenter.empty` instead of a sentence. A zh/en parity test cannot
 * catch that (both sides were equally missing), so this test walks the literal
 * `t("...")` calls in the React sources and resolves each one against the zh
 * dictionary.
 *
 * Dynamic keys (`t(\`x.${y}\`)`, variables, ternaries) are out of scope: only
 * string literals are resolved, which is where typos and forgotten entries live.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import zh from "./zh";

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CALL = /\bt\(\s*(["'])([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+)\1/g;

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) out.push(full);
  }
  return out;
}

function resolveKey(key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => {
    if (!node || typeof node !== "object") return undefined;
    return (node as Record<string, unknown>)[part];
  }, zh);
}

describe("i18n keys used by the UI", () => {
  it("defines every literal t(\"…\") key in the zh dictionary", () => {
    const missing: string[] = [];
    for (const file of walk(SRC)) {
      const source = fs.readFileSync(file, "utf8");
      for (const match of source.matchAll(CALL)) {
        const key = match[2];
        // `ra.*` belongs to react-admin's own dictionary, and a literal ending in
        // `_` is a prefix concatenated at runtime (e.g. `dialog_` + variant).
        if (key.startsWith("ra.") || key.endsWith("_")) continue;
        if (typeof resolveKey(key) !== "string") {
          missing.push(`${path.relative(SRC, file)}: ${key}`);
        }
      }
    }
    expect(missing).toEqual([]);
  });
});
