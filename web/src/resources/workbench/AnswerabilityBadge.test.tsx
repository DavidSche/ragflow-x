/**
 * doc/301 T12 (QA-1) — the per-turn honesty badge.
 *
 * The badge's whole job is to not lie in three specific ways, so that is what these
 * tests pin: a missing claim is not a pass, an unknown server value is shown as itself
 * rather than swallowed, and the English build has no Chinese of its own.
 */
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AnswerabilityBadge } from "./AnswerabilityBadge";
import { answerabilityState, answerabilityTooltip } from "./answerability";
import { tZh, translateFor } from "../../test/translate";

const locale = vi.hoisted(() => ({ current: "zh" as "zh" | "en" }));

vi.mock("ra-core", () => ({
  useTranslate: () => (locale.current === "en" ? translateFor("en") : tZh),
}));

const CJK = /[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/;

describe("AnswerabilityBadge", () => {
  beforeEach(() => {
    locale.current = "zh";
  });

  it("renders `not recorded` when the turn has no claim", () => {
    const { container } = render(<AnswerabilityBadge claim={null} />);
    expect(container.querySelector("[data-answerability]")?.getAttribute("data-answerability")).toBe(
      "not_recorded",
    );
    expect(screen.getByText(tZh("qa.answerability.not_recorded"))).toBeInTheDocument();
    // A missing measurement must not be painted as a passing one.
    expect(screen.queryByText(tZh("qa.answerability.answerable"))).not.toBeInTheDocument();
  });

  it("renders a grounded answer", () => {
    render(<AnswerabilityBadge claim={{ answerable: true, abstain_reason: "none" }} />);
    expect(screen.getByText(tZh("qa.answerability.answerable"))).toBeInTheDocument();
  });

  it("keeps a refusal distinguishable from an empty corpus", () => {
    const { unmount } = render(
      <AnswerabilityBadge claim={{ answerable: false, abstain_reason: "self_abstain" }} />,
    );
    expect(screen.getByText(tZh("qa.answerability.self_abstain"))).toBeInTheDocument();
    expect(screen.queryByText(tZh("qa.answerability.no_evidence"))).not.toBeInTheDocument();
    unmount();

    render(<AnswerabilityBadge claim={{ answerable: false, abstain_reason: "no_evidence" }} />);
    expect(screen.getByText(tZh("qa.answerability.no_evidence"))).toBeInTheDocument();
  });

  it("shows an unknown server value as itself", () => {
    // A closed set that grows must not render as a blank badge or a generic "unknown":
    // the reader has to see which value arrived before anyone can decide about it.
    const { container } = render(<AnswerabilityBadge claim={{ abstain_reason: "retrieval_timeout" }} />);
    expect(container.querySelector("[data-answerability]")?.getAttribute("data-answerability")).toBe(
      "retrieval_timeout",
    );
    expect(screen.getByText("retrieval_timeout")).toBeInTheDocument();
  });

  it("offers a tooltip only where the dictionary has one", () => {
    const withTooltip = render(<AnswerabilityBadge claim={{ abstain_reason: "no_evidence" }} />);
    expect(withTooltip.container.querySelector("[data-answerability]")?.getAttribute("title")).toBe(
      tZh("qa.answerability.tooltip_no_evidence"),
    );
    withTooltip.unmount();

    // `low_similarity` has no dedicated sentence: an absent tooltip is better than a
    // title reading the key.
    const withoutTooltip = render(<AnswerabilityBadge claim={{ abstain_reason: "low_similarity" }} />);
    expect(withoutTooltip.container.querySelector("[data-answerability]")?.getAttribute("title")).toBeNull();
    expect(answerabilityTooltip(tZh, "low_similarity")).toBeUndefined();
  });

  it("renders no Chinese of its own in the en locale", () => {
    locale.current = "en";
    const { container } = render(
      <AnswerabilityBadge claim={{ answerable: false, abstain_reason: "no_evidence", citation_count: 0 }} />,
    );
    expect(container.textContent ?? "").not.toMatch(CJK);
    expect(container.textContent ?? "").toContain("No evidence retrieved");
  });
});

describe("answerabilityState", () => {
  it("treats an answerable claim without a reason as answerable, and anything else as unrecorded", () => {
    expect(answerabilityState({ answerable: true })).toBe("answerable");
    // `answerable:false` with no reason cannot be named: showing "not recorded" is true,
    // showing "no evidence" would be an invention.
    expect(answerabilityState({ answerable: false })).toBe("not_recorded");
    expect(answerabilityState(undefined)).toBe("not_recorded");
  });
});
