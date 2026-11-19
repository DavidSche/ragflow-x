/**
 * R19 template governance (doc/295 §2.3, A58/A59).
 *
 * Three rules the panel exists to make visible, each asserted here:
 *  - a key may have many **immutable** versions; the line shows the newest and how
 *    many exist, because "edit" means "publish N+1", never "overwrite N";
 *  - a `platform` row is read-only *by design*, so the panel prints the reason
 *    instead of an action that would answer 400 (`UpdateReportTemplate` only ever
 *    writes a workspace row);
 *  - retiring is not deleting: the note says historical revisions still resolve by
 *    their own version number;
 *  - **"edit" opens the structure editor** (doc/288 §8.7): `PUT /report-templates/:key`
 *    existed since P2 while every customisation needed `curl`. The blind "publish the
 *    row back" button is gone because re-sending identical content is an idempotent
 *    replay (`created=false`) — it could never mint the version its label promised;
 *  - **"new template" covers the create verb**, which had the same gap one step
 *    earlier: cloning a platform key was the only way to get a workspace template, so
 *    a workspace could never add a report type the platform does not ship.
 */
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportTemplatePanel, groupTemplateRows } from "./ReportTemplatePanel";
import type { ReportTemplateRow } from "./report-center-api";
import { tZh } from "../../test/translate";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
  apiPost: vi.fn(),
}));

const identity = vi.hoisted(() => ({ tenant_id: "tenant-1" }));

vi.mock("ra-core", () => ({
  useTranslate: () => tZh,
  useLocaleState: () => ["zh", vi.fn()],
  useGetIdentity: () => ({ data: identity }),
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, put: mocks.apiPut, post: mocks.apiPost },
  ApiError: class extends Error {
    readonly displayMessage = "模板不可用（400/44105）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

function row(overrides: Partial<ReportTemplateRow>): ReportTemplateRow {
  return {
    template_id: "tpl-1",
    template_key: "analysis",
    version: 1,
    tenant_id: "tenant-platform",
    payload: { name: { zh: "风险分析报告", en: "Risk analysis" } },
    status: "active",
    retired: false,
    source: "platform",
    editable: false,
    ...overrides,
  };
}

// `GET /report-templates` answers **one row per key** (newest wins), so the fixture
// is two rows, not five. The version count travels as `versions` because counting the
// rows in hand would always say 1 (live 2026-11-19: "v2（共 1 个版本）").
const rows: ReportTemplateRow[] = [
  row({
    template_id: "tpl-w3",
    template_key: "analysis",
    version: 3,
    versions: 3,
    tenant_id: "tenant-1",
    source: "workspace",
    editable: true,
    source_template_id: "tpl-p1",
    source_template_version: 1,
  }),
  row({
    template_id: "tpl-p2",
    template_key: "compliance",
    version: 1,
    versions: 1,
    payload: { name: { zh: "合规自查报告", en: "Compliance self-check" } },
  }),
];

function ok(data: unknown) {
  return Promise.resolve({ data: { code: 0, data } });
}

describe("groupTemplateRows", () => {
  it("collapses a key to its newest version and reads the version count", () => {
    const groups = groupTemplateRows(rows);
    expect(groups.map((group) => group.key)).toEqual(["analysis", "compliance"]);
    // v3 is the version a new report would pin; v1 and v2 are history the list does
    // not ship, so the count has to come from the server.
    expect(groups[0].newest.version).toBe(3);
    expect(groups[0].versions).toBe(3);
    expect(groups[0].retired).toBe(false);
  });

  it("falls back to the row count when the server does not report versions", () => {
    // An older server sends every version as its own row and no `versions` field.
    const groups = groupTemplateRows([
      row({ template_key: "old", version: 1 }),
      row({ template_key: "old", version: 2, tenant_id: "tenant-1", source: "workspace", editable: true }),
    ]);
    expect(groups[0].newest.version).toBe(2);
    expect(groups[0].versions).toBe(2);
  });

  it("treats a key as retired only when every version is", () => {
    const allRetired = groupTemplateRows([
      row({ template_key: "old", version: 2, status: "retired", retired: true }),
      row({ template_key: "old", version: 1, status: "retired", retired: true }),
    ]);
    expect(allRetired[0].retired).toBe(true);
    // One live version among retired ones keeps the key usable.
    const mixed = groupTemplateRows([
      row({ template_key: "old", version: 2, status: "retired", retired: true }),
      row({ template_key: "old", version: 1 }),
    ]);
    expect(mixed[0].retired).toBe(false);
  });

  it("drops rows with no key instead of rendering an empty line", () => {
    expect(groupTemplateRows([row({ template_key: "" })])).toEqual([]);
  });
});

describe("ReportTemplatePanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.apiGet.mockImplementation(async () => ok(rows));
  });

  it("shows provenance, version count and status without raw enum values", async () => {
    render(<ReportTemplatePanel canManage />);

    expect(await screen.findByText("风险分析报告")).toBeTruthy();
    // The newest row is the workspace fork, so that is the source shown.
    expect(screen.getByTestId("template-source-analysis").textContent).toBe("工作区自建");
    expect(screen.getByTestId("template-version-analysis").textContent).toBe("v3（共 3 个版本）");
    expect(screen.getByTestId("template-status-analysis").textContent).toBe("在用");
    expect(screen.getByTestId("template-source-compliance").textContent).toBe("平台内置");
    expect(screen.queryByText("workspace")).toBeNull();
    expect(screen.queryByText("platform")).toBeNull();
    expect(screen.queryByText("retired")).toBeNull();
  });

  it("offers a platform template the fork, not an edit", async () => {
    render(<ReportTemplatePanel canManage />);

    // `compliance` exists only upstream: forking is the only move.
    expect(await screen.findByTestId("template-clone-compliance")).toBeTruthy();
    expect(screen.queryByTestId("template-edit-compliance")).toBeNull();
    expect(screen.queryByTestId("template-retire-compliance")).toBeNull();
    // `analysis` is the workspace's own newest version, so it can move forward.
    expect(screen.getByTestId("template-edit-analysis")).toBeTruthy();
    expect(screen.queryByTestId("template-clone-analysis")).toBeNull();
  });

  it("explains why a platform baseline cannot be edited", async () => {
    mocks.apiGet.mockImplementation(async () =>
      ok([row({ template_key: "compliance", payload: { name: { zh: "合规自查报告" } } })]),
    );
    render(<ReportTemplatePanel canManage />);

    expect(await screen.findByText("平台内置模板不能在工作区修改。")).toBeTruthy();
  });

  it("opens the structure editor on the newest version's payload", async () => {
    const user = userEvent.setup();
    render(<ReportTemplatePanel canManage />);

    await user.click(await screen.findByTestId("template-edit-analysis"));

    // Seeded from *published* content, and the dialog says the save publishes N+1.
    expect(await screen.findByTestId("template-editor")).toBeTruthy();
    expect((screen.getByTestId("template-editor-name-zh") as HTMLInputElement).value).toBe(
      "风险分析报告",
    );
    expect(screen.getByText("编辑模板结构（analysis）")).toBeTruthy();
    expect(mocks.apiPut).not.toHaveBeenCalled();
  });

  it("publishes the edited structure and reports the version the server minted", async () => {
    mocks.apiPut.mockImplementation(async () => ok({ template: { version: 4 }, created: true }));
    const user = userEvent.setup();
    render(<ReportTemplatePanel canManage />);

    await user.click(await screen.findByTestId("template-edit-analysis"));
    await screen.findByTestId("template-editor");
    // This row's payload has no sections (a seeded row read from a half-authored
    // fixture), so name the one section it needs before saving.
    await user.click(screen.getByTestId("template-editor-add-section"));
    await user.type(screen.getByTestId("template-editor-section-0-key"), "body");
    await user.type(screen.getByTestId("template-editor-section-0-title"), "正文");
    await user.type(screen.getByTestId("template-editor-section-0-prompt"), "写正文。");
    await user.click(screen.getByTestId("template-editor-section-0-evidence"));
    await user.click(screen.getByTestId("template-editor-save"));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPut.mock.calls[0];
    expect(url).toBe("/report-templates/analysis");
    // `key` travels in the body too: path and body must name one template.
    expect(body.key).toBe("analysis");
    expect(body.payload.sections).toHaveLength(1);
    expect(body.payload.sections[0].key).toBe("body");
    // The dialog closes and the notice quotes the server's version, not a client +1.
    await waitFor(() => expect(screen.queryByTestId("template-editor")).toBeNull());
    expect((await screen.findByTestId("report-template-notice")).textContent).toBe("已保存为 v4。");
    // The list is reloaded so the new version number is the server's, not a guess.
    expect(mocks.apiGet).toHaveBeenCalledTimes(2);
  });

  it("creates a new workspace template key through the editor", async () => {
    mocks.apiPost.mockImplementation(async () => ok({ template: { version: 1 }, created: true }));
    const user = userEvent.setup();
    render(<ReportTemplatePanel canManage />);

    await user.click(await screen.findByTestId("template-create"));
    // The heading is the dialog's own title, so the form cannot be mistaken for an
    // edit of the row above it.
    expect((await screen.findByRole("dialog")).textContent).toContain("新建模板");
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
    // The create verb is keyed from the body: `POST /report-templates`.
    expect(url).toBe("/report-templates");
    expect(body.key).toBe("report-vendor-risk");
    expect(body.payload.sections[0].key).toBe("background");
    await waitFor(() => expect(screen.queryByTestId("template-editor")).toBeNull());
    expect((await screen.findByTestId("report-template-notice")).textContent).toBe("已保存为 v1。");
  });

  it("hides the create entry inside the platform tenant and says why", async () => {
    // `PublishReportTemplate` refuses `PlatformTenantID`: a runtime write there would
    // change every workspace's baseline at once (live 2026-11-19, 400/44105).
    identity.tenant_id = "00000000000000000000000000000000";
    render(<ReportTemplatePanel canManage />);

    expect(await screen.findByTestId("report-template-panel")).toBeTruthy();
    expect(screen.queryByTestId("template-create")).toBeNull();
    expect(screen.getByTestId("template-create-blocked").textContent).toContain("平台租户");
    identity.tenant_id = "tenant-1";
  });

  it("hides the create entry from a caller who cannot manage templates", async () => {
    render(<ReportTemplatePanel canManage={false} />);
    expect(await screen.findByTestId("report-template-panel")).toBeTruthy();
    expect(screen.queryByTestId("template-create")).toBeNull();
  });

  it("retires a live workspace key and reloads", async () => {
    mocks.apiPost.mockImplementation(async () => ok({}));
    const user = userEvent.setup();
    render(<ReportTemplatePanel canManage />);

    await user.click(await screen.findByTestId("template-retire-analysis"));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/report-templates/analysis/retire"));
    expect(mocks.apiGet).toHaveBeenCalledTimes(2);
  });

  it("forks a platform key into the workspace", async () => {
    mocks.apiPost.mockImplementation(async () => ok({}));
    const user = userEvent.setup();
    render(<ReportTemplatePanel canManage />);

    await user.click(await screen.findByTestId("template-clone-compliance"));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/report-templates/compliance/clone"));
  });

  it("keeps the list readable for a caller without manage but hides every action", async () => {
    render(<ReportTemplatePanel canManage={false} />);

    expect(await screen.findByText("风险分析报告")).toBeTruthy();
    expect(screen.queryByTestId("template-edit-analysis")).toBeNull();
    expect(screen.queryByTestId("template-retire-analysis")).toBeNull();
    expect(screen.queryByTestId("template-clone-compliance")).toBeNull();
    // The reason still prints: "read-only" is information, not a missing button.
    expect(screen.getByText("当前账号不能修改模板。")).toBeTruthy();
  });

  it("says retirement only affects new reports", async () => {
    render(<ReportTemplatePanel canManage />);
    expect(
      await screen.findByText(
        "退休只影响新建报告：历史版本仍按原版本号解析，不改写已发布的正文。",
      ),
    ).toBeTruthy();
  });

  it("renders an empty list as a sentence", async () => {
    mocks.apiGet.mockImplementation(async () => ok([]));
    render(<ReportTemplatePanel canManage />);
    expect(await screen.findByText("还没有可用的模板。")).toBeTruthy();
  });

  it("renders a failed read as a sentence", async () => {
    mocks.apiGet.mockResolvedValue({ data: { code: 1, data: null } });
    render(<ReportTemplatePanel canManage />);
    expect((await screen.findByTestId("report-template-error")).textContent).toBe("模板列表读取失败。");
  });
});
