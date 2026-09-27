// ScenarioID: SC-WORKBENCH-001
import { describe, expect, test } from "vitest";
import {
  classifyConversationError,
  feedbackRequestId,
  citationsFromReference,
  extractCitedIds,
  extractCitations,
  isSafeSourceUri,
  mergeCitations,
  streamRead,
  type Citation,
} from "./workbench-types";

describe("feedbackRequestId", () => {
  test("maps assistant turn ids back to their completion request id", () => {
    expect(feedbackRequestId("assistant-run-123")).toBe("run-123");
  });

  test("preserves workbench turn ids and rejects empty values", () => {
    expect(feedbackRequestId("turn-123")).toBe("turn-123");
    expect(feedbackRequestId("")).toBe("");
    expect(feedbackRequestId()).toBe("");
  });
});

describe("extractCitedIds", () => {
  test("collects distinct cited ids in ascending order", () => {
    expect(extractCitedIds("\u770b [ID:3] \u548c [ID:0ID:2]")).toEqual([0, 2, 3]);
  });

  test("returns an empty array when nothing is cited", () => {
    expect(extractCitedIds("no references here")).toEqual([]);
    expect(extractCitedIds("")).toEqual([]);
  });
});

describe("extractCitations", () => {
  test("returns [] for empty input", () => {
    expect(extractCitations(null)).toEqual([]);
    expect(extractCitations({})).toEqual([]);
  });

  test("maps plain list entries to citations", () => {
    const out = extractCitations({
      reference: [
        { doc_name: "a.pdf", content: "chunk-a", dataset_id: "ds1", doc_id: "doc1", source_uri: "https://example.com/doc" },
        { name: "empty", content: "" },
        "plain string",
      ],
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ name: "a.pdf", content: "chunk-a", datasetId: "ds1", docId: "doc1" });
    expect(out[0].sourceUri).toBe("https://example.com/doc");
  });

  test("reads chunks from an object reference", () => {
    const out = extractCitations({
      citations: { chunks: [{ chunk_id: "c1", content: "ccc" }] },
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ name: "c1", chunkId: "c1" });
  });
});

describe("isSafeSourceUri", () => {
  test("allows same-origin and HTTPS sources only", () => {
    expect(isSafeSourceUri("https://example.com/manual")).toBe(true);
    expect(isSafeSourceUri("/documents/manual.pdf")).toBe(true);
    expect(isSafeSourceUri(window.location.href)).toBe(true);
    expect(isSafeSourceUri("http://example.com/manual")).toBe(false);
    expect(isSafeSourceUri("javascript:alert(1)")).toBe(false);
    expect(isSafeSourceUri("not a url")).toBe(false);
  });
});

describe("citationsFromReference", () => {
  test("returns [] without chunks", () => {
    expect(citationsFromReference(null)).toEqual([]);
    expect(citationsFromReference({})).toEqual([]);
    expect(citationsFromReference({ chunks: [] })).toEqual([]);
  });

  test("maps chunks with image ids", () => {
    const out = citationsFromReference({
      chunks: [{ document_name: "d.pdf", content: "body", image_id: "img-1" }],
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ name: "d.pdf", content: "body", imageId: "img-1" });
  });

  test("assigns deterministic citation ids", () => {
    const reference = {
      chunks: [
        { chunk_id: "chunk-1", document_name: "d.pdf", content: "body" },
        { document_name: "d.pdf", content: "body" },
      ],
    };
    const first = citationsFromReference(reference);
    const second = citationsFromReference(reference);
    expect(first[0].id).toBe("chunk-1");
    expect(first[1].id).toBe(second[1].id);
  });
});

describe("mergeCitations", () => {
  test("appends only new citations", () => {
    const base: Citation[] = [{ name: "a", content: "keep" }];
    const incoming: Citation[] = [
      { name: "a", content: "keep" },
      { name: "b", content: "new" },
      { name: "b", content: "" },
    ];
    expect(mergeCitations(base, incoming)).toHaveLength(2);
  });
});

describe("streamRead", () => {
  test("maps conversation errors to stable protocol codes", () => {
    expect(classifyConversationError(403, "forbidden")).toBe("permission_denied");
    expect(classifyConversationError(429, "too many requests")).toBe("gateway_rate_limited");
    expect(classifyConversationError(502, "bad gateway")).toBe("model_failure");
    expect(classifyConversationError(undefined, "Empty response body")).toBe("no_result");
    expect(classifyConversationError(undefined, "Empty response body", 0)).toBe("insufficient_citation");
    expect(classifyConversationError(400, "invalid request")).toBe("model_failure");
  });

  function sseStream(payloads: string[]): ReadableStream<Uint8Array> {
    const encoder = new TextEncoder();
    return new ReadableStream<Uint8Array>({
      start(controller) {
        for (const p of payloads) controller.enqueue(encoder.encode(p));
        controller.close();
      },
    });
  }

  test("concatenates answer deltas and stops at [DONE]", async () => {
    const deltas: string[] = [];
    const refs: Citation[][] = [];
    await streamRead(
      sseStream([
        'data: {"data":{"answer":"Hel"}}\n',
        'data: {"data":{"answer":"lo"}}\n',
        "data: [DONE]\n",
      ]),
      (d, r) => {
        deltas.push(d);
        refs.push(r);
      },
    );
    expect(deltas.join("")).toBe("Hello");
    expect(refs).toEqual([[], []]);
  });

  test("extracts citations from a data frame", async () => {
    const refs: Citation[][] = [];
    await streamRead(
      sseStream([
        'data: {"data":{"answer":"ok","reference":[{"content":"c1","name":"n1"}]}}\n',
      ]),
      (_d, r) => refs.push(r),
    );
    expect(refs[0]).toHaveLength(1);
    expect(refs[0][0].name).toBe("n1");
  });

  test("forwards OpenAI-compatible error frames to onError", async () => {
    const errors: string[] = [];
    await streamRead(
      sseStream(['data: {"error":{"message":"boom"}}\n']),
      () => {},
      (m) => errors.push(m),
    );
    expect(errors).toEqual(["boom"]);
  });

  test("parses fallback answer / choices frames", async () => {
    const deltas: string[] = [];
    await streamRead(
      sseStream([
        'data: {"answer":"fallback"}\n',
        'data: {"choices":[{"delta":{"content":" choice"}}]}\n',
      ]),
      (d) => deltas.push(d),
    );
    expect(deltas.join("")).toBe("fallback choice");
  });

  test("decodes multi-byte UTF-8 chunks split across stream boundaries", async () => {
    const deltas: string[] = [];
    const encoder = new TextEncoder();
    const answer = encoder.encode('data: {"data":{"answer":"中文回答"}}\n');
    const splitAt = answer.findIndex((byte) => byte === 0xad) + 1;
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(answer.slice(0, splitAt));
        controller.enqueue(answer.slice(splitAt));
        controller.enqueue(encoder.encode("data: [DONE]\n"));
        controller.close();
      },
    });

    await streamRead(stream, (delta) => deltas.push(delta));
    expect(deltas.join("")).toBe("中文回答");
  });
});
