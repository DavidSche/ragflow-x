import type { Message } from "./workbench-types";

export type ConversationExportFormat = "markdown" | "html" | "json";

const esc = (value: string): string =>
  value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");

const inlineMarkdownToHtml = (line: string): string => {
  const escaped = esc(line);
  return escaped
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
};

const markdownToSafeHtml = (markdown: string): string =>
  markdown
    .split(/\r?\n/)
    .map((line) => {
      if (!line.trim()) return "";
      const heading = line.match(/^(#{1,6})\s+(.*)$/);
      if (heading) {
        const level = Math.min(6, heading[1].length + 2);
        return `<h${level}>${inlineMarkdownToHtml(heading[2])}</h${level}>`;
      }
      if (/^\s*[-*]\s+/.test(line)) return `<p>• ${inlineMarkdownToHtml(line.replace(/^\s*[-*]\s+/, ""))}</p>`;
      if (/^\s*\d+\.\s+/.test(line)) return `<p>${inlineMarkdownToHtml(line)}</p>`;
      return `<p>${inlineMarkdownToHtml(line)}</p>`;
    })
    .join("\n");

export function conversationToMarkdown(messages: Message[], title: string): string {
  const lines: string[] = [`# ${title}`, ""];
  const latest = messages.reduce((latest, message) => message.createdAt && message.createdAt > latest ? message.createdAt : latest, "");
  if (latest) lines.push(`exported_at: \`${latest}\``, "");
  for (const m of messages) {
    if (!m.content.trim() && !m.error) continue;
    const who = m.role === "user" ? "用户" : m.role === "assistant" ? "AI" : "系统";
    lines.push(`## ${who}`, "", m.content.trim() || (m.error ? `错误：${m.error}` : ""), "");
    if (m.citations?.length) {
      lines.push("### 引用", "");
      m.citations.forEach((c, i) => {
        lines.push(`${i + 1}. **${c.name}**：${c.content}`);
      });
      lines.push("");
    }
    if (m.traceId) {
      lines.push(`trace_id: \`${m.traceId}\``, "");
    }
    if (m.model) {
      lines.push(`model: \`${m.model}\``, "");
    }
    if (m.createdAt) {
      lines.push(`time: \`${m.createdAt}\``, "");
    }
    if (m.usage?.totalTokens) {
      lines.push(`tokens: \`${m.usage.totalTokens}\``, "");
    }
  }
  return lines.join("\n");
}

export function conversationToHtml(messages: Message[], title: string): string {
  const body = messages
    .filter((m) => m.content.trim() || m.error)
    .map((m) => {
      const who = m.role === "user" ? "用户" : m.role === "assistant" ? "AI" : "系统";
      const cites = m.citations?.length
        ? `<ol>${m.citations.map((c) => `<li><strong>${esc(c.name)}</strong>：${esc(c.content)}</li>`).join("")}</ol>`
        : "";
      const trace = [
        m.createdAt ? `<small>time: <time>${esc(m.createdAt)}</time></small>` : "",
        m.traceId ? `<small>trace_id: <code>${esc(m.traceId)}</code></small>` : "",
        m.model ? `<small>model: <code>${esc(m.model)}</code></small>` : "",
        m.usage?.totalTokens ? `<small>tokens: <code>${esc(String(m.usage.totalTokens))}</code></small>` : "",
      ].filter(Boolean).join(" · ");
      const traceHtml = trace ? `<p>${trace}</p>` : "";
      return `<section><h2>${esc(who)}</h2>${markdownToSafeHtml(m.content.trim() || (m.error ? `错误：${m.error}` : ""))}${cites}${traceHtml}</section>`;
    })
    .join("\n");
  return `<!doctype html><html><head><meta charset="utf-8"><title>${esc(title)}</title><style>body{font-family:system-ui,-apple-system,sans-serif;max-width:760px;margin:2rem auto;padding:0 1rem;line-height:1.6}pre{white-space:pre-wrap;background:#f5f5f5;padding:1rem;border-radius:.5rem}</style></head><body><h1>${esc(title)}</h1>${body}</body></html>`;
}

export function downloadConversation(
  messages: Message[],
  title: string,
  format: ConversationExportFormat,
  filenamePrefix = "conversation",
): void {
  const safeTitle = title.replace(/[\\/:*?"<>|]/g, "-").trim() || filenamePrefix;
  let blob: Blob;
  let filename: string;
  if (format === "markdown") {
    blob = new Blob([conversationToMarkdown(messages, title)], { type: "text/markdown;charset=utf-8" });
    filename = `${safeTitle}.md`;
  } else if (format === "html") {
    blob = new Blob([conversationToHtml(messages, title)], { type: "text/html;charset=utf-8" });
    filename = `${safeTitle}.html`;
  } else {
    blob = new Blob([JSON.stringify({
      schema: "ragflow-x.conversation.v1",
      title,
      exportedAt: new Date().toISOString(),
      messages,
    }, null, 2)], { type: "application/json;charset=utf-8" });
    filename = `${safeTitle}.json`;
  }
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
