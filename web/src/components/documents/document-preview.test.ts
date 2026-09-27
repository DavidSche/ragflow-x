import { describe, expect, it } from "vitest";
import {
  isDecodedTextKind,
  officePreviewKind,
  parseDelimited,
  prettyJson,
  previewFormatKind,
} from "./document-preview";

describe("previewFormatKind", () => {
  it("maps RAGFlow document families by MIME type", () => {
    const cases: Array<[string, string, Parameters<typeof previewFormatKind>[0]]> = [
      ["application/pdf", "", "pdf"],
      ["application/vnd.openxmlformats-officedocument.wordprocessingml.document", "", "docx"],
      ["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", "xlsx"],
      ["application/vnd.openxmlformats-officedocument.presentationml.presentation", "", "pptx"],
      ["text/markdown", "", "markdown"],
      ["text/html", "", "html"],
      ["application/json", "", "json"],
      ["text/csv", "", "csv"],
      ["text/xml", "", "xml"],
      ["message/rfc822", "", "email"],
      ["image/png", "", "image"],
      ["audio/mpeg", "", "audio"],
      ["video/mp4", "", "video"],
      ["text/plain", "", "text"],
    ];
    for (const [contentType, filename, expected] of cases) {
      expect(previewFormatKind(contentType, filename)).toBe(expected);
    }
  });

  it("uses extensions when the engine returns a generic MIME type", () => {
    expect(previewFormatKind("application/octet-stream", "policy.md")).toBe("markdown");
    expect(previewFormatKind("application/octet-stream", "table.tsv")).toBe("csv");
    expect(previewFormatKind("application/octet-stream", "record.eml")).toBe("email");
    expect(previewFormatKind("application/octet-stream", "photo.svg")).toBe("image");
    expect(previewFormatKind("application/octet-stream", "config.yaml")).toBe("text");
  });

  it("keeps unsupported binary formats in the download fallback", () => {
    expect(previewFormatKind("application/octet-stream", "legacy.doc")).toBeNull();
    expect(previewFormatKind("application/octet-stream", "legacy.ppt")).toBeNull();
    expect(previewFormatKind("application/octet-stream", "email.msg")).toBeNull();
    expect(previewFormatKind("application/vnd.ms-outlook", "email.msg")).toBeNull();
    expect(officePreviewKind("application/octet-stream", "legacy.doc")).toBeNull();
  });

  it("identifies formats that need decoded text", () => {
    expect(isDecodedTextKind("markdown")).toBe(true);
    expect(isDecodedTextKind("csv")).toBe(true);
    expect(isDecodedTextKind("pdf")).toBe(false);
  });
});

describe("structured text helpers", () => {
  it("pretty prints JSON and JSONL without dropping malformed input", () => {
    expect(prettyJson('{"name":"安全"}')).toBe('{\n  "name": "安全"\n}');
    expect(prettyJson('{"name":"安全"}\n{"name":"合规"}')).toContain('"name": "安全"');
    expect(prettyJson("not-json")).toBe("not-json");
  });

  it("parses quoted CSV and TSV rows", () => {
    expect(parseDelimited('name,note\n"政策,修订","引号""内"')).toEqual([
      ["name", "note"],
      ["政策,修订", '引号"内'],
    ]);
    expect(parseDelimited("risk\tstatus\nhigh\tclosed", "\t")).toEqual([
      ["risk", "status"],
      ["high", "closed"],
    ]);
    expect(parseDelimited("a;b\n1;2").length).toBe(2);
  });
});
