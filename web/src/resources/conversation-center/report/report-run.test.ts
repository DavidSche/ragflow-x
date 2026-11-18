/**
 * T8 runtime unit tests — SSE frame parsing, streaming reader, context builder,
 * localStorage job mapping, and the event poller (including the 44115 cursor
 * reset path). No DOM required.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  isReportJobTerminal,
  openReportEventPoller,
  parseReportSseFrame,
  readReportSse,
  rememberReportJob,
  rememberedReportJob,
  reportContextFromRecords,
  type CreateReportResult,
  type ReportDetailRecord,
} from "./report-run";

describe("parseReportSseFrame", () => {
  it("parses a data: line into a frame", () => {
    const frame = parseReportSseFrame(
      `data: {"seq":3,"type":"job.progress","job_id":"j1","revision_id":"r1","at":"2026-11-17T10:00:00Z","payload":{"done":1,"total":5}}`,
    );
    expect(frame).not.toBeNull();
    expect(frame!.seq).toBe(3);
    expect(frame!.type).toBe("job.progress");
    expect(frame!.job_id).toBe("j1");
    expect(frame!.revision_id).toBe("r1");
    expect(frame!.payload).toEqual({ done: 1, total: 5 });
  });

  it("ignores comments, id/event metadata and empty data lines", () => {
    expect(parseReportSseFrame(": keepalive")).toBeNull();
    expect(parseReportSseFrame("id: 3")).toBeNull();
    expect(parseReportSseFrame("event: report.job")).toBeNull();
    expect(parseReportSseFrame("data:")).toBeNull();
    expect(parseReportSseFrame("data:   ")).toBeNull();
    expect(parseReportSseFrame("not a data line")).toBeNull();
  });

  it("returns null for malformed or non-conforming JSON", () => {
    expect(parseReportSseFrame("data: {not json")).toBeNull();
    expect(parseReportSseFrame(`data: {"seq":"3","type":"x"}`)).toBeNull();
    expect(parseReportSseFrame(`data: {"seq":3}`)).toBeNull();
  });

  it("tolerates a missing payload object", () => {
    const frame = parseReportSseFrame(`data: {"seq":4,"type":"outline.ready","job_id":"j1","revision_id":"r1"}`);
    expect(frame).not.toBeNull();
    expect(frame!.payload).toEqual({});
  });
});

describe("readReportSse", () => {
  async function collect(body: ReadableStream<Uint8Array>): Promise<unknown[]> {
    const frames: unknown[] = [];
    for await (const frame of readReportSse(body)) frames.push(frame);
    return frames;
  }

  it("parses multiple frames across chunk boundaries", async () => {
    const chunk = new TextEncoder().encode(
      `data: {"seq":1,"type":"outline.ready","job_id":"j1","revision_id":"r1"}\n` +
        `data: {"seq":2,"type":"section.start","job_id":"j1","revision_id":"r1"}\n\n`,
    );
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        // Split mid-frame to exercise the buffered carrier line.
        controller.enqueue(chunk.subarray(0, 40));
        controller.enqueue(chunk.subarray(40));
        controller.close();
      },
    });
    const frames = await collect(stream);
    expect(frames).toHaveLength(2);
    expect((frames[0] as { seq: number }).seq).toBe(1);
    expect((frames[1] as { seq: number }).seq).toBe(2);
  });
});

describe("reportContextFromRecords", () => {
  const createResult: CreateReportResult = {
    created: true,
    report: {
      id: "rep1",
      title: "Q3 质量报告",
      question: "汇总三季度质量",
      template_key: "tpl-x",
      status: "running",
      access_policy: "owner_only",
      current_revision_id: "rev1",
      created_at: "2026-11-17T10:00:00.000Z",
      updated_at: "2026-11-17T10:00:00.000Z",
    },
    revision: {
      id: "rev1",
      report_id: "rep1",
      revision: 1,
      based_on_revision: 0,
      outline_json: "[]",
      outline_confirmation: "required",
      datasets_json: "[]",
      template_key: "tpl-x",
      template_version: 1,
      engine_version: "v1",
      metrics_json: "{}",
      gap_json: "{}",
      status: "awaiting_outline",
      completeness: "pending",
      published_at: null,
      superseded_at: null,
      created_at: "2026-11-17T10:00:00.000Z",
    },
    job: {
      id: "job1",
      status: "queued",
      job_kind: "report",
    },
  };

  it("builds a run-kind context with job status precedence", () => {
    const context = reportContextFromRecords(createResult, { jobId: "job1" });
    expect(context.kind).toBe("report");
    expect(context.type).toBe("run");
    expect(context.id).toBe("rep1");
    expect(context.targetId).toBe("tpl-x");
    expect(context.title).toBe("Q3 质量报告");
    expect(context.reportStatus).toBe("queued");
    expect(context.jobId).toBe("job1");
    expect(context.updatedAt).toBe(Date.parse("2026-11-17T10:00:00.000Z"));
  });

  it("falls back to revision status when the record has no job", () => {
    const detail = {
      created: false,
      report: createResult.report,
      revision: createResult.revision,
    } as unknown as ReportDetailRecord;
    const context = reportContextFromRecords(detail);
    expect(context.kind).toBe("report");
    expect(context.reportStatus).toBe("awaiting_outline");
    expect(context.jobId).toBeUndefined();
  });
});

describe("isReportJobTerminal", () => {
  it.each(["succeeded", "partial", "failed", "cancelled"])("accepts %s", (status) => {
    expect(isReportJobTerminal(status)).toBe(true);
  });
  it.each(["queued", "running", "awaiting_outline", "finalizing", "cancel_requested", undefined, ""])(
    "rejects %s",
    (status) => {
      expect(isReportJobTerminal(status)).toBe(false);
    },
  );
});

describe("rememberReportJob / rememberedReportJob", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("round-trips a report→job mapping through storage", () => {
    expect(rememberedReportJob("rep1")).toBeUndefined();
    rememberReportJob("rep1", "job9");
    expect(rememberedReportJob("rep1")).toBe("job9");
    expect(rememberedReportJob("other")).toBeUndefined();
  });
});

describe("openReportEventPoller", () => {
  const sseBody = (lines: string[]): Response =>
    new Response(new TextEncoder().encode(lines.map((l) => `data: ${l}\n`).join("")), {
      status: 200,
    });

  const sseFrame = (seq: number, type: string): string =>
    JSON.stringify({ seq, type, job_id: "j1", revision_id: "r1", at: "2026-11-17T10:00:00Z" });

  async function drain(poller: ReturnType<typeof openReportEventPoller>): Promise<string[]> {
    const types: string[] = [];
    for await (const frame of poller.frames()) types.push(frame.type);
    return types;
  }

  it("yields frames and stops at a terminal event", async () => {
    const calls: string[] = [];
    const transport = vi.fn(async (url: string) => {
      calls.push(url);
      return sseBody([sseFrame(1, "job.progress"), sseFrame(2, "section.succeeded"), sseFrame(3, "job.succeeded")]);
    });
    const poller = openReportEventPoller("j1", { transport });
    const types = await drain(poller);
    expect(types).toEqual(["job.progress", "section.succeeded", "job.succeeded"]);
    expect(poller.lastCursor).toBe(3);
    expect(calls).toEqual(["/api/v1/report-jobs/j1/events"]);
  });

  it("resumes from the `after` cursor when provided", async () => {
    const transport = vi.fn(async (url: string) => sseBody([sseFrame(7, "job.failed")]));
    const poller = openReportEventPoller("j1", { after: 6, transport });
    const types = await drain(poller);
    expect(types).toEqual(["job.failed"]);
    expect(transport).toHaveBeenCalledWith(
      "/api/v1/report-jobs/j1/events?after=6",
      expect.anything(),
    );
  });

  it("sends the cursor back as Last-Event-ID (A31b / §8.3)", async () => {
    // A `fetch`-driven reconnect does not get the browser's EventSource resumption,
    // so the header is set explicitly: a resume must be legible to the server and to
    // any proxy that records it, not only as a query string.
    const transport = vi.fn(async (url: string, init?: RequestInit) => {
      void init;
      return sseBody([sseFrame(7, "job.failed")]);
    });
    const poller = openReportEventPoller("j1", { after: 6, transport });
    await drain(poller);
    const init = transport.mock.calls[0]?.[1];
    expect(init?.headers).toEqual({ "Last-Event-ID": "6" });
  });

  it("omits Last-Event-ID for a full replay", async () => {
    // `after=0` means "everything", and a `Last-Event-ID: 0` header would be a lie
    // about a cursor that was never handed out.
    const transport = vi.fn(async (url: string, init?: RequestInit) => {
      void init;
      return sseBody([sseFrame(1, "job.failed")]);
    });
    const poller = openReportEventPoller("j1", { transport });
    await drain(poller);
    const init = transport.mock.calls[0]?.[1];
    expect(init?.headers).toBeUndefined();
  });

  it("handles a 44115 cursor reset by asking onReset for the resume seq", async () => {
    const onReset = vi.fn(async () => 12);
    const transport = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ code: 44115, data: { min_seq: 10, event_seq: 12 } }), {
          status: 409,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(sseBody([sseFrame(12, "job.succeeded")]));
    const poller = openReportEventPoller("j1", { transport, onReset });
    const types = await drain(poller);
    expect(types).toEqual(["job.succeeded"]);
    expect(onReset).toHaveBeenCalledWith({ min_seq: 10, event_seq: 12 });
    expect(transport).toHaveBeenNthCalledWith(
      2,
      "/api/v1/report-jobs/j1/events?after=12",
      expect.anything(),
    );
    expect(poller.lastCursor).toBe(12);
  });

  it("reconnects after a non-terminal close and continues from the last cursor", async () => {
    const transport = vi
      .fn()
      .mockResolvedValueOnce(sseBody([sseFrame(4, "section.succeeded")]))
      .mockResolvedValueOnce(sseBody([sseFrame(5, "job.succeeded")]));
    const poller = openReportEventPoller("j1", { transport });
    const types = await drain(poller);
    expect(types).toEqual(["section.succeeded", "job.succeeded"]);
    expect(transport).toHaveBeenCalledTimes(2);
    expect(transport).toHaveBeenNthCalledWith(
      2,
      "/api/v1/report-jobs/j1/events?after=4",
      expect.anything(),
    );
  });

  it("aborting the poller stops frames mid-stream", async () => {
    const transport = vi.fn(async () => sseBody([sseFrame(1, "job.progress"), sseFrame(2, "section.succeeded")]));
    const poller = openReportEventPoller("j1", { transport });
    const iterator = poller.frames();
    const first = await iterator.next();
    expect(first.value.type).toBe("job.progress");
    poller.abort();
    const next = await iterator.next();
    expect(next.done).toBe(true);
    expect(poller.aborted).toBe(true);
  });
});
