import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translateFor, tZh } from "../../test/translate";
import { AnswerQualityProxyCard } from "./AnswerQualityProxyCard";

// The dictionary is resolved for real (`tZh`), never mocked as `(key) => key`: the whole
// point of these assertions is that a reader sees 「暂无判定」 and not a key name.
vi.mock("ra-core", () => ({
  useTranslate: () => translateFor("zh"),
}));

const apiGet = vi.fn();

vi.mock("../../lib/api", () => ({
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

const envelope = <T,>(data: T) => Promise.resolve({ data: { code: 0, data } });

function bucket(overrides: Record<string, unknown> = {}) {
  return {
    turns: 10,
    answered: 8,
    abstained: 2,
    citations_missing: 1,
    feedback_answered: 2,
    negative_answered: 1,
    abstain_rate: 0.2,
    citation_missing_rate: 0.125,
    false_answer_proxy: 0.5,
    feedback_coverage: 0.25,
    similarity_samples: 8,
    similarity_p50: 0.512,
    similarity_p90: 0.874,
    ...overrides,
  };
}

function payload(weeks: Array<Record<string, unknown>>) {
  return {
    window: { from: "2026-02-16T00:00:00Z", to: "2026-03-02T00:00:00Z" },
    weeks: weeks.map((week, index) => ({
      week_start: index === 0 ? "2026-02-16T00:00:00Z" : "2026-02-23T00:00:00Z",
      week_end: index === 0 ? "2026-02-23T00:00:00Z" : "2026-03-02T00:00:00Z",
      ...bucket(week),
    })),
    totals: bucket(weeks[weeks.length - 1] ?? {}),
    bias: {
      methodology: "online_proxy",
      week_basis: "utc_monday",
      feedback_coverage: 0.25,
      unlabelled_answered: 6,
      window_weeks: weeks.length,
      weeks_clamped: false,
    },
  };
}

describe("<AnswerQualityProxyCard />", () => {
  beforeEach(() => {
    apiGet.mockReset();
  });

  it("prints every rate next to the denominator it was computed from", async () => {
    apiGet.mockReturnValue(envelope(payload([{}])));
    render(<AnswerQualityProxyCard weeks={4} />);

    await waitFor(() => expect(screen.getByTestId("answer-quality-proxy")).toBeTruthy());
    expect(apiGet).toHaveBeenCalledWith("/knowledge-ops/answer-quality?weeks=4");

    // 20.0% abstention over 2/10 turns — the numbers, not just the percentage.
    const abstain = screen.getByTestId("quality-cell-abstain_rate");
    expect(abstain.textContent).toContain("20.0%");
    expect(abstain.textContent).toContain("2/10");

    // The unsourced rate divides *answered* turns (1/8), never all turns: the two
    // denominators differ and both are on screen.
    const unsourced = screen.getByTestId("quality-cell-citation_missing_rate");
    expect(unsourced.textContent).toContain("12.5%");
    expect(unsourced.textContent).toContain("1/8");

    // The wrong-answer proxy divides rated answers (1/2), and coverage divides answered
    // turns (2/8) — a reader can see that 50% rests on two ratings.
    const wrong = screen.getByTestId("quality-cell-false_answer_proxy");
    expect(wrong.textContent).toContain("50.0%");
    expect(wrong.textContent).toContain("1/2");
    const coverage = screen.getByTestId("quality-cell-feedback_coverage");
    expect(coverage.textContent).toContain("25.0%");
    expect(coverage.textContent).toContain("2/8");

    // Quantiles print three decimals and carry their sample count.
    const p50 = screen.getByTestId("quality-cell-similarity_p50");
    expect(p50.textContent).toContain("0.512");
    expect(p50.textContent).toContain("8");
  });

  it("renders an empty window as 暂无判定 rather than 0%", async () => {
    apiGet.mockReturnValue(
      envelope(
        payload([
          {
            turns: 0,
            answered: 0,
            abstained: 0,
            citations_missing: 0,
            feedback_answered: 0,
            negative_answered: 0,
            abstain_rate: null,
            citation_missing_rate: null,
            false_answer_proxy: null,
            feedback_coverage: null,
            similarity_samples: 0,
            similarity_p50: null,
            similarity_p90: null,
          },
        ]),
      ),
    );
    render(<AnswerQualityProxyCard />);

    const abstain = await screen.findByTestId("quality-cell-abstain_rate");
    expect(abstain.textContent).toContain(tZh("knowledgeOps.quality_ratio_unavailable"));
    expect(abstain.textContent).not.toContain("0.0%");
    expect(screen.getByTestId("quality-cell-false_answer_proxy").textContent).not.toContain("0.0%");
    expect(screen.getByTestId("quality-cell-similarity_p50").textContent).toContain(
      tZh("knowledgeOps.quality_ratio_unavailable"),
    );
  });

  it("prints the proxy caveat with the coverage it cannot see", async () => {
    apiGet.mockReturnValue(envelope(payload([{}, {}])));
    render(<AnswerQualityProxyCard />);

    expect(await screen.findByTestId("quality-bias-coverage")).toBeTruthy();
    const bias = screen.getByTestId("quality-bias-coverage");
    // Coverage rate *and* the unrated population, because "误答率 50%" alone would look
    // like a fact about all answered turns when it is a fact about two of them.
    expect(bias.textContent).toContain("25.0%");
    expect(bias.textContent).toContain("6");
    expect(screen.getByText(tZh("knowledgeOps.quality_methodology_online_proxy"))).toBeTruthy();
    expect(screen.getByText(tZh("knowledgeOps.quality_week_basis_utc_monday"))).toBeTruthy();
    // A trend table needs one row per week plus a pooled total row.
    expect(screen.getByText("2026-02-16")).toBeTruthy();
    expect(screen.getByText(tZh("knowledgeOps.quality_week_total"))).toBeTruthy();
  });

  // A 200 whose shape is not this card's payload is a failed load, not a crash: the card
  // shares a page with six other panels and must not take them down with it.
  it("treats an unexpected payload shape as a failed load", async () => {
    apiGet.mockReturnValue(envelope({ total_turns: 3, citation_rate: 0.5 }));
    render(<AnswerQualityProxyCard />);

    await waitFor(() =>
      expect(screen.getByText(tZh("knowledgeOps.quality_load_failed"))).toBeTruthy(),
    );
    expect(screen.queryByTestId("quality-cell-abstain_rate")).toBeNull();
  });

  it("says nothing when the load fails instead of showing stale numbers", async () => {
    apiGet.mockRejectedValue(new Error("boom"));
    render(<AnswerQualityProxyCard />);

    await waitFor(() =>
      expect(screen.getByText(tZh("knowledgeOps.quality_load_failed"))).toBeTruthy(),
    );
    expect(screen.queryByTestId("quality-cell-abstain_rate")).toBeNull();
  });
});
