export type DocumentPreviewKind =
  | "audio"
  | "csv"
  | "docx"
  | "email"
  | "html"
  | "image"
  | "json"
  | "markdown"
  | "pdf"
  | "pptx"
  | "text"
  | "video"
  | "xlsx"
  | "xml";

const FORMAT_EXTENSIONS: Record<DocumentPreviewKind, string[]> = {
  audio: ["aac", "flac", "m4a", "mp3", "ogg", "opus", "wav"],
  csv: ["csv", "tsv"],
  docx: ["docx"],
  email: ["eml"],
  html: ["htm", "html", "xhtml"],
  image: ["avif", "bmp", "gif", "jpeg", "jpg", "png", "svg", "webp"],
  json: ["geojson", "json", "jsonl", "ndjson"],
  markdown: ["markdown", "md"],
  pdf: ["pdf"],
  pptx: ["pptx"],
  text: [
    "adoc", "asciidoc", "c", "cfg", "conf", "cpp", "cs", "css", "dart", "diff",
    "go", "h", "hpp", "ini", "java", "js", "json5", "jsx", "kt", "less", "log",
    "php", "properties", "py", "rb", "rs", "rst", "sass", "scss", "sh", "sql",
    "toml", "ts", "tsx", "txt", "yaml", "yml",
  ],
  video: ["avi", "m4v", "mkv", "mov", "mp4", "mpeg", "mpg", "webm"],
  xlsx: ["xlsx", "xlsm"],
  xml: ["rss", "xml", "xsl", "xslt"],
};

const MIME_PATTERNS: Array<[DocumentPreviewKind, RegExp]> = [
  ["audio", /^audio\//],
  ["csv", /^(text\/(csv|tab-separated-values)|application\/csv)/],
  ["docx", /wordprocessingml/],
  ["email", /^message\/(rfc822|news)/],
  ["html", /^(text\/html|application\/xhtml\+xml)/],
  ["image", /^image\//],
  ["json", /(json|geo\+json)/],
  ["markdown", /markdown/],
  ["pdf", /pdf/],
  ["pptx", /presentationml/],
  ["xml", /^(text\/xml|application\/(xml|rss\+xml)|image\/svg\+xml)/],
  ["text", /^(text\/|application\/(javascript|typescript|x-yaml|toml))/],
  ["video", /^video\//],
  ["xlsx", /spreadsheetml/],
];

export function fileExtension(filename?: string): string {
  const name = filename ?? "";
  const index = name.lastIndexOf(".");
  return index >= 0 ? name.slice(index + 1).toLowerCase() : "";
}

export function previewFormatKind(
  contentType: string,
  filename?: string,
): DocumentPreviewKind | null {
  const mime = contentType.toLowerCase();
  for (const [kind, pattern] of MIME_PATTERNS) {
    if (pattern.test(mime)) return kind;
  }
  const extension = fileExtension(filename);
  if (!extension) return null;
  for (const [kind, extensions] of Object.entries(FORMAT_EXTENSIONS)) {
    if ((extensions as string[]).includes(extension)) return kind as DocumentPreviewKind;
  }
  return null;
}

export function officePreviewKind(
  contentType: string,
  filename?: string,
): Extract<DocumentPreviewKind, "docx" | "xlsx" | "pptx"> | null {
  const kind = previewFormatKind(contentType, filename);
  return kind === "docx" || kind === "xlsx" || kind === "pptx" ? kind : null;
}

export function markdownPreview(contentType: string, filename?: string): boolean {
  return previewFormatKind(contentType, filename) === "markdown";
}

export function isDecodedTextKind(kind: DocumentPreviewKind | null): boolean {
  return kind === "csv" || kind === "email" || kind === "json" || kind === "markdown" ||
    kind === "text" || kind === "xml";
}

export function isTextPreview(contentType: string, filename?: string): boolean {
  return isDecodedTextKind(previewFormatKind(contentType, filename));
}

export function prettyJson(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    const lines = trimmed.split(/\r?\n/).filter((line) => line.trim());
    if (lines.length > 1 && lines.every((line) => {
      try {
        JSON.parse(line);
        return true;
      } catch {
        return false;
      }
    })) {
      return lines.map((line) => JSON.stringify(JSON.parse(line), null, 2)).join("\n");
    }
    return raw;
  }
}

export function parseDelimited(raw: string, forcedDelimiter?: string): string[][] {
  const text = raw.replace(/^\uFEFF/, "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const firstLine = text.split("\n", 1)[0] ?? "";
  const delimiter = forcedDelimiter ?? (
    [",", ";", "\t"].map((candidate) => ({
      candidate,
      count: countUnquoted(firstLine, candidate),
    })).sort((left, right) => right.count - left.count)[0]?.candidate ?? ","
  );
  const rows: string[][] = [];
  let row: string[] = [];
  let value = "";
  let quoted = false;

  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quoted) {
      if (char === '"' && text[index + 1] === '"') {
        value += '"';
        index += 1;
      } else if (char === '"') {
        quoted = false;
      } else {
        value += char;
      }
      continue;
    }
    if (char === '"' && value === "") {
      quoted = true;
    } else if (char === delimiter) {
      row.push(value);
      value = "";
    } else if (char === "\n") {
      row.push(value);
      rows.push(row);
      row = [];
      value = "";
    } else {
      value += char;
    }
  }
  if (value || row.length) {
    row.push(value);
    rows.push(row);
  }
  return rows.filter((row) => row.length > 1 || row[0] !== "");
}

function countUnquoted(value: string, delimiter: string): number {
  let count = 0;
  let quoted = false;
  for (let index = 0; index < value.length; index += 1) {
    const char = value[index];
    if (char === '"') quoted = !quoted;
    if (!quoted && char === delimiter) count += 1;
  }
  return count;
}
