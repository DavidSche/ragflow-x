import type { ReactNode } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

type TokenType = "key" | "string" | "number" | "keyword";

type Token = {
  type: TokenType;
  text: string;
};

const tokenClasses: Record<TokenType, string> = {
  key: "text-sky-700 dark:text-sky-300",
  string: "text-emerald-700 dark:text-emerald-300",
  number: "text-amber-700 dark:text-amber-300",
  keyword: "text-purple-700 dark:text-purple-300",
};

const JSON_TOKEN_PATTERN = new RegExp(
  [
    String.raw`"(?:\\.|[^"\\])*"(?=\s*:)`,
    String.raw`"(?:\\.|[^"\\])*"`,
    String.raw`-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?`,
    String.raw`true|false|null`,
  ].join("|"),
  "g",
);

export function tokenizeJSON(text: string): Token[] {
  const tokens: Token[] = [];
  let lastIndex = 0;
  JSON_TOKEN_PATTERN.lastIndex = 0;
  for (const match of text.matchAll(JSON_TOKEN_PATTERN)) {
    const index = match.index ?? 0;
    if (index > lastIndex) {
      tokens.push({ type: "keyword", text: text.slice(lastIndex, index) });
    }
    const value = match[0];
    const type: TokenType = value.startsWith('"')
      ? text[index + value.length]?.trimStart()?.startsWith(":") ? "key" : "string"
      : /^(true|false|null)$/.test(value) ? "keyword" : "number";
    tokens.push({ type, text: value });
    lastIndex = index + value.length;
  }
  if (lastIndex < text.length) {
    tokens.push({ type: "keyword", text: text.slice(lastIndex) });
  }
  return tokens;
}

export function ApprovalPayloadField({
  id,
  label,
  value,
  invalidLabel,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  invalidLabel: string;
  onChange: (value: string) => void;
}) {
  let parsed: unknown;
  let invalid = false;
  try {
    parsed = JSON.parse(value || "{}");
  } catch (error) {
    invalid = true;
  }

  const preview = invalid ? null : JSON.stringify(parsed, null, 2);
  const tokens = tokenizeJSON(preview ?? "");

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Textarea
        id={id}
        rows={6}
        className="font-mono"
        value={value}
        aria-invalid={invalid}
        onChange={(event) => onChange(event.target.value)}
      />
      {preview ? (
        <pre
          aria-label={`${label} preview`}
          className="max-h-48 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-5"
        >
          {tokens.map((token, index) => (
            <span key={index} className={tokenClasses[token.type]}>{token.text}</span>
          ))}
        </pre>
      ) : (
        <p role="alert" className="text-xs text-destructive">{invalidLabel}</p>
      )}
    </div>
  );
}
