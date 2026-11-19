import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportSharePanel } from "./ReportSharePanel";
import type { ReportAccessView } from "./report-center-api";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
}));

// Only the strings this panel renders are resolved; anything else stays a key, so a
// test cannot pass by matching copy that moved.
const DICT: Record<string, string> = {
  "reportCenter.share_title": "分享",
  "reportCenter.share_hint": "决定谁能读到这份报告。",
  "reportCenter.share_edit": "调整分享范围",
  "reportCenter.share_fail": "分享范围读取或保存失败。",
  "reportCenter.share_unavailable": "当前服务端未返回分享信息。",
  "reportCenter.share_policy": "可见范围",
  "reportCenter.share_pick_users": "选择成员",
  "reportCenter.share_pick_teams": "选择团队",
  "reportCenter.access_grantee_missing": "已失效",
  "reportCenter.share_replace_hint": "保存会整体替换名单。",
  "reportCenter.share_save": "保存分享范围",
  "reportCenter.share_readonly_scope": "取消运行、重新生成、删除报告仍仅限所有者。",
  "reportCenter.label_access_policy.owner_only": "仅自己",
  "reportCenter.label_access_policy.tenant_members": "工作区成员",
  "reportCenter.label_access_policy.selected_users": "指定成员",
  "reportCenter.label_access_policy.selected_groups": "指定团队",
  "reportCenter.label_grant_kind.user": "成员",
  "reportCenter.label_grant_kind.team": "团队",
  "ra.action.cancel": "取消",
};

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string, options?: Record<string, string>) => {
    const template = DICT[key] ?? key;
    if (!options) return template;
    return Object.entries(options).reduce(
      (text, [name, value]) => text.split(`%{${name}}`).join(value),
      template,
    );
  },
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, put: mocks.apiPut },
  ApiError: class extends Error {
    readonly displayMessage = "分享范围无效（400/44105）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

const ownerOnly: ReportAccessView = {
  policy: "owner_only",
  is_owner: true,
  can_manage: true,
  grantees: [],
};

const sharedWithUser: ReportAccessView = {
  policy: "selected_users",
  is_owner: true,
  can_manage: true,
  grantees: [{ kind: "user", id: "u-2", name: "张三", missing: false }],
};

const inheritedMarker: ReportAccessView = {
  policy: "selected_groups",
  is_owner: false,
  can_manage: false,
  grantees: [{ kind: "team", id: "team-gone", name: "", missing: true }],
};

describe("ReportSharePanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.apiGet.mockImplementation((path: string) => {
      if (path === "/users") {
        return Promise.resolve({
          data: { code: 0, data: { items: [{ id: "u-2", username: "张三" }, { id: "u-3", username: "李四" }] } },
        });
      }
      return Promise.resolve({ data: { code: 0, data: [{ id: "team-1", name: "财务组" }] } });
    });
    mocks.apiPut.mockResolvedValue({ data: { code: 0, data: sharedWithUser } });
  });

  it("prints the policy in words and names the grantees", async () => {
    render(<ReportSharePanel reportId="rep-1" access={sharedWithUser} />);

    expect((await screen.findByTestId("report-share-current")).textContent).toBe("指定成员");
    const grantees = await screen.findByTestId("report-share-grantees");
    expect(grantees.textContent).toContain("成员");
    expect(grantees.textContent).toContain("张三");
    // A share never widens writes, and the panel says so rather than leaving the
    // reader to assume otherwise.
    expect(screen.getByText("取消运行、重新生成、删除报告仍仅限所有者。")).toBeTruthy();
  });

  it("marks a grantee whose id no longer resolves instead of dropping it", async () => {
    render(<ReportSharePanel reportId="rep-1" access={inheritedMarker} />);

    const grantees = await screen.findByTestId("report-share-grantees");
    // The id is never printed; the row stays visible so the owner can see the policy
    // still names somebody.
    expect(grantees.textContent).toContain("已失效");
    expect(grantees.textContent).not.toContain("team-gone");
    // A reader gets no editor at all.
    expect(screen.queryByTestId("report-share-edit")).toBeNull();
  });

  it("says so when the server sends no sharing state", () => {
    render(<ReportSharePanel reportId="rep-1" access={undefined} />);

    expect(screen.getByTestId("report-share-unavailable").textContent).toBeTruthy();
    expect(screen.queryByTestId("report-share-edit")).toBeNull();
  });

  it("writes the whole list for the chosen policy", async () => {
    render(<ReportSharePanel reportId="rep-1" access={ownerOnly} onSaved={() => {}} />);
    const user = userEvent.setup();

    expect((await screen.findByTestId("report-share-current")).textContent).toBe("仅自己");
    await user.click(screen.getByTestId("report-share-edit"));
    fireEvent.change(screen.getByTestId("report-share-policy"), {
      target: { value: "selected_users" },
    });
    // The member list arrives with names, never ids: the checkbox is a person.
    await user.click(await screen.findByTestId("report-share-user-u-2"));
    await user.click(screen.getByTestId("report-share-save"));

    await waitFor(() =>
      expect(mocks.apiPut).toHaveBeenCalledWith("/reports/rep-1/access", {
        policy: "selected_users",
        user_ids: ["u-2"],
      }),
    );
    // The answer is the server's view, so the panel cannot disagree with what was
    // stored.
    await waitFor(() =>
      expect((screen.getByTestId("report-share-current")).textContent).toBe("指定成员"),
    );
  });

  it("sends no grantee list for a policy that takes none", async () => {
    render(<ReportSharePanel reportId="rep-1" access={sharedWithUser} />);
    const user = userEvent.setup();

    await user.click(await screen.findByTestId("report-share-edit"));
    fireEvent.change(screen.getByTestId("report-share-policy"), {
      target: { value: "tenant_members" },
    });
    await user.click(screen.getByTestId("report-share-save"));

    await waitFor(() =>
      expect(mocks.apiPut).toHaveBeenCalledWith("/reports/rep-1/access", {
        policy: "tenant_members",
      }),
    );
  });

  it("shows the server's refusal instead of pretending the share was saved", async () => {
    mocks.apiPut.mockRejectedValue(new Error("boom"));
    render(<ReportSharePanel reportId="rep-1" access={ownerOnly} />);
    const user = userEvent.setup();

    await user.click(await screen.findByTestId("report-share-edit"));
    await user.click(screen.getByTestId("report-share-save"));

    expect((await screen.findByTestId("report-share-error")).textContent).toBe(
      "分享范围读取或保存失败。",
    );
    // The editor stays open: the user's edit is not thrown away by a failure.
    expect(screen.getByTestId("report-share-save")).toBeTruthy();
  });
});
