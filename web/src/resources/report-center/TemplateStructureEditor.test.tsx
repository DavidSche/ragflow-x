/**
 * R19 structure editor (doc/288 §8.7 "有接口无入口", doc/295 §2.3).
 *
 * Three things this file has to prove, because each was a way the surface could
 * look finished while being unusable:
 *  - the form is seeded from what is *published*, so "save" means "publish this
 *    edited payload", not "publish the row back";
 *  - an invalid draft never reaches the server (`400/44105` is the boundary, not the
 *    feedback channel), and the message names the section;
 *  - `auto` cannot be saved without naming who may use it (292 D2), because that is
 *    also what the payload validator on the server refuses.
 */
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TemplateStructureEditor } from "./TemplateStructureEditor";
import { tZh } from "../../test/translate";

const mocks = vi.hoisted(() => ({
  apiPut: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => tZh,
}));

vi.mock("../../lib/api", () => {
  // The same shape the real wrapper throws, so "a 44105 prints as a sentence" is
  // tested against the class the panel actually catches.
  class ApiError extends Error {
    readonly displayMessage: string;
    constructor(status: number, code: number, message: string) {
      super(`${status}/${code}`);
      this.displayMessage = message;
    }
  }
  return {
    api: { put: mocks.apiPut, post: mocks.apiPost },
    ApiError,
    unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
      const response = await request;
      if (response.data.code !== 0) {
        throw new ApiError(400, response.data.code, "模板结构不合法（400/44105）");
      }
      return response.data.data;
    },
  };
});

const payload = {
  name: { zh: "制度符合性分析报告", en: "Compliance analysis report" },
  output_formats: ["docx", "md"],
  sections: [
    {
      key: "background",
      title: "背景与范围",
      prompt: "说明评估范围。",
      requires_evidence: false,
      max_words: 300,
    },
    {
      key: "compliance",
      title: "逐条符合性判定",
      prompt: "逐条判定符合性。",
      requires_evidence: true,
      max_words: 1200,
      depends_on: ["background"],
    },
  ],
  limits: {
    max_sections: 10,
    slice_max_seconds: 480,
    total_max_seconds: 0,
    max_slices: 8,
    max_tokens_per_section: 1600,
  },
  retrieval: { top_k: 8, threshold_policy: "dataset_calibrated", min_evidence: 1 },
  evidence_policy: {
    conflict_handling: "disclose",
    outdated_evidence: "flag",
    unsupported_claims: "prohibit",
  },
  outline_confirmation: "required",
};

function ok(data: unknown) {
  return Promise.resolve({ data: { code: 0, data } });
}

function renderEditor(onSaved = vi.fn(), onCancel = vi.fn()) {
  render(
    <TemplateStructureEditor
      mode="edit"
      templateKey="report-compliance"
      payload={payload}
      onSaved={onSaved}
      onCancel={onCancel}
    />,
  );
  return { onSaved, onCancel };
}

describe("TemplateStructureEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.apiPut.mockImplementation(async () => ok({ template: { version: 3 }, created: true }));
    mocks.apiPost.mockImplementation(async () => ok({ template: { version: 1 }, created: true }));
  });

  it("opens on the published content, including section order and dependencies", () => {
    renderEditor();

    expect((screen.getByTestId("template-editor-name-zh") as HTMLInputElement).value).toBe(
      "制度符合性分析报告",
    );
    expect((screen.getByTestId("template-editor-section-0-key") as HTMLInputElement).value).toBe(
      "background",
    );
    expect((screen.getByTestId("template-editor-section-1-title") as HTMLInputElement).value).toBe(
      "逐条符合性判定",
    );
    expect(
      (screen.getByTestId("template-editor-section-1-dep-background") as HTMLInputElement).checked,
    ).toBe(true);
    // Output formats are a closed set and print as words, not as `docx`.
    expect((screen.getByTestId("template-editor-format-docx") as HTMLInputElement).checked).toBe(true);
    expect((screen.getByTestId("template-editor-format-pdf") as HTMLInputElement).checked).toBe(false);
    expect(screen.getByText("Word（docx）")).toBeTruthy();
    // A valid seed means the save button is live from the first render.
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(false);
  });

  it("publishes an edited payload under the same key and reports the server's version", async () => {
    const user = userEvent.setup();
    const { onSaved } = renderEditor();

    const title = screen.getByTestId("template-editor-section-1-title");
    await user.clear(title);
    await user.type(title, "逐条符合性判定（修订）");
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPut.mock.calls[0];
    expect(url).toBe("/report-templates/report-compliance");
    // `key` travels in the body as well: the path and the body must name one template.
    expect(body.key).toBe("report-compliance");
    expect(body.payload.sections[1].title).toBe("逐条符合性判定（修订）");
    // The rest of the payload is carried through, so a save is not a partial write.
    expect(body.payload.limits.max_slices).toBe(8);
    expect(body.payload.sections[1].depends_on).toEqual(["background"]);
    expect(body.payload).not.toHaveProperty("auto_confirm_allowed_roles");
    // The version number is the server's answer, never a client-side +1 — and
    // `created` travels with it so the caller can tell a mint from a replay.
    expect(onSaved).toHaveBeenCalledWith({ version: 3, created: true });
  });

  it("refuses to submit an invalid draft and says which section is wrong", async () => {
    const user = userEvent.setup();
    renderEditor();

    await user.clear(screen.getByTestId("template-editor-section-0-prompt"));

    expect((await screen.findByTestId("template-editor-errors")).textContent).toContain(
      "第 1 节的写作要求不能为空。",
    );
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(true);
    await user.click(screen.getByTestId("template-editor-save"));
    // Clicking a disabled button must still not reach the network.
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("warns before the last evidence-carrying section is turned off", async () => {
    const user = userEvent.setup();
    renderEditor();

    await user.click(screen.getByTestId("template-editor-section-1-evidence"));

    expect((await screen.findByTestId("template-editor-errors")).textContent).toContain(
      "至少要有一节需要证据，否则报告会退化成观点文。",
    );
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(true);
  });

  it("asks who may skip the review when outline confirmation is switched off", async () => {
    const user = userEvent.setup();
    renderEditor();

    await user.selectOptions(screen.getByTestId("template-editor-outline-confirmation"), "auto");
    expect((await screen.findByTestId("template-editor-errors")).textContent).toContain(
      "选择「允许自动确认」时必须指定可自动确认的角色。",
    );
    expect(screen.queryByTestId("template-editor-auto-role-operator")).toBeTruthy();

    await user.click(screen.getByTestId("template-editor-auto-role-operator"));
    expect(screen.queryByTestId("template-editor-errors")).toBeNull();
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const body = mocks.apiPut.mock.calls[0][1];
    expect(body.payload.outline_confirmation).toBe("auto");
    expect(body.payload.auto_confirm_allowed_roles).toEqual(["operator"]);
  });

  it("adds, removes and reorders sections, and the payload follows the array", async () => {
    const user = userEvent.setup();
    renderEditor();

    await user.click(screen.getByTestId("template-editor-add-section"));
    expect(screen.getByTestId("template-editor-section-2")).toBeTruthy();
    // A blank section has no key yet, so the draft is invalid until it is named.
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(true);
    await user.type(screen.getByTestId("template-editor-section-2-key"), "remediation");
    await user.type(screen.getByTestId("template-editor-section-2-title"), "整改建议");
    await user.type(screen.getByTestId("template-editor-section-2-prompt"), "给出整改动作。");
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(false);

    // "Up" on the third section makes it second: order is the array, so the payload
    // is the proof.
    await user.click(screen.getByTestId("template-editor-section-2-up"));
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const keys = mocks.apiPut.mock.calls[0][1].payload.sections.map(
      (section: { key: string }) => section.key,
    );
    expect(keys).toEqual(["background", "remediation", "compliance"]);
  });

  it("removes a section and drops the dependency that pointed at it", async () => {
    const user = userEvent.setup();
    renderEditor();

    // Removing `background` leaves `compliance` depending on a key that is gone; the
    // draft drops that dependency instead of publishing an un-resolvable reference.
    await user.click(screen.getByTestId("template-editor-section-0-remove"));
    expect(screen.queryByTestId("template-editor-section-1")).toBeNull();
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(false);
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const [section] = mocks.apiPut.mock.calls[0][1].payload.sections;
    expect(section.key).toBe("compliance");
    expect(section).not.toHaveProperty("depends_on");
  });

  it("prints a server refusal instead of a raw exception", async () => {
    const user = userEvent.setup();
    mocks.apiPut.mockResolvedValue({ data: { code: 44105, data: null } });
    renderEditor();

    await user.click(screen.getByTestId("template-editor-save"));

    expect((await screen.findByTestId("template-editor-error")).textContent).toBe(
      "模板结构不合法（400/44105）",
    );
  });

  it("creates a brand-new key from one empty section instead of forking a platform one", async () => {
    const user = userEvent.setup();
    render(
      <TemplateStructureEditor mode="create" onSaved={vi.fn()} onCancel={vi.fn()} />,
    );

    // No payload means no sections, but the form starts with one row so "add a
    // section" is an edit rather than a prerequisite.
    expect(screen.getByTestId("template-editor-section-0")).toBeTruthy();
    expect((screen.getByTestId("template-editor-key") as HTMLInputElement).value).toBe("");
    expect((screen.getByTestId("template-editor-save") as HTMLButtonElement).disabled).toBe(true);

    // A key is contract-shaped: `Report_Compliance` would be refused by the server.
    await user.type(screen.getByTestId("template-editor-key"), "Report_Compliance");
    expect((await screen.findByTestId("template-editor-errors")).textContent).toContain(
      "模板 key 必须是小写 slug",
    );

    await user.clear(screen.getByTestId("template-editor-key"));
    await user.type(screen.getByTestId("template-editor-key"), "report-vendor-risk");
    await user.type(screen.getByTestId("template-editor-name-zh"), "供应商风险报告");
    await user.type(screen.getByTestId("template-editor-name-en"), "Vendor risk report");
    await user.type(screen.getByTestId("template-editor-section-0-key"), "background");
    await user.type(screen.getByTestId("template-editor-section-0-title"), "背景");
    await user.type(screen.getByTestId("template-editor-section-0-prompt"), "说明背景。");
    await user.click(screen.getByTestId("template-editor-section-0-evidence"));
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPost.mock.calls[0];
    expect(url).toBe("/report-templates");
    expect(body.key).toBe("report-vendor-risk");
    expect(body.payload.sections).toHaveLength(1);
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("reports a byte-identical save as a replay, not as a new version", async () => {
    const user = userEvent.setup();
    // `created=false` is what the server answers when the payload hashes the same.
    mocks.apiPut.mockImplementation(async () => ok({ template: { version: 2 }, created: false }));
    const { onSaved } = renderEditor();

    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ version: 2, created: false }));
  });

  it("closes without publishing when cancelled", async () => {
    const user = userEvent.setup();
    const { onCancel } = renderEditor();

    await user.click(screen.getByText("取消"));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });
});
