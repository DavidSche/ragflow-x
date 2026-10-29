import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AssistantEmptyState } from "./AssistantEmptyState";

const access = vi.hoisted(() => ({ map: {} as Record<string, boolean | undefined> }));
const permissions = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
const readiness = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useCreatePath: () => ({ resource, type }: { resource: string; type: string }) => `/${resource}/${type}`,
  usePermissions: () => ({ data: permissions.value, isPending: false }),
  useCanAccess: ({ resource, action }: { resource: string; action: string }) => ({
    canAccess: access.map[`${resource}:${action}`],
    isPending: false,
  }),
}));

vi.mock("../trial-runs/types", () => ({
  getOnboardingReadiness: readiness.get,
}));

function renderEmptyState(assistantId = "chat-1") {
  return render(
    <MemoryRouter>
      <AssistantEmptyState assistantId={assistantId} />
    </MemoryRouter>,
  );
}

describe("AssistantEmptyState permission-aware calls to action", () => {
  beforeEach(() => {
    access.map = {};
    permissions.value = {};
    readiness.get.mockReset();
    readiness.get.mockResolvedValue({
      assistant_id: "chat-1",
      completed: 1,
      total: 4,
      milestones: [{ key: "dataset_ready", done: true }],
    });
  });

  it("offers a business user the assistants they can use plus a copyable request note instead of template links", async () => {
    // business_user holds scenario-template read (doc/242 migration 158) but
    // neither chat:manage nor approval:execute (doc/240), so /approvals/create
    // would be a 403 dead end: it must not be linked.
    access.map = { "dataset:read": true, "chat:manage": false, "scenario-template:read": true, "approval:execute": false };
    permissions.value = { approvalsEnabled: true };

    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    renderEmptyState();

    expect(screen.getByText("conversationCenter.requestAssistant.title")).toBeInTheDocument();
    const viewMine = screen.getByText("conversationCenter.requestAssistant.viewMine").closest("a");
    expect(viewMine).toHaveAttribute("href", "/conversation-center/list");
    expect(screen.queryByText("conversationCenter.requestAssistant.request")).toBeNull();
    const requestLinks = screen
      .queryAllByRole("link")
      .filter((node) => node.getAttribute("href") === "/approvals/create");
    expect(requestLinks).toHaveLength(0);

    fireEvent.click(screen.getByText("conversationCenter.requestAssistant.copyRequest"));
    expect(writeText).toHaveBeenCalledWith("conversationCenter.requestAssistant.copyBody");
    expect(screen.getByText("conversationCenter.requestAssistant.copied")).toBeInTheDocument();

    // The wizard milestone still lands on a 403 (chat:manage), so it stays
    // hidden; the trial milestone is readable and its progress now loads.
    expect(screen.queryByText(/scenarioTemplates\.readiness\.assistant_ready$/)).toBeNull();
    expect(screen.getByText(/scenarioTemplates\.readiness\.trial_completed$/)).toBeInTheDocument();
    expect(await screen.findByText(/scenarioTemplates\.readiness\.progress/)).toBeInTheDocument();
    expect(readiness.get).toHaveBeenCalledWith("chat-1");
  });

  it("stays silent when a not-yet-migrated deployment still withholds scenario-template read", () => {
    // Safety net for migration 158 not having run: the read gate is what makes
    // the readiness request legal, so without it nothing may be fetched.
    access.map = { "dataset:read": true, "chat:manage": false, "scenario-template:read": false, "approval:execute": false };
    permissions.value = { approvalsEnabled: true };

    renderEmptyState();

    expect(screen.queryByText(/scenarioTemplates\.readiness\.assistant_ready$/)).toBeNull();
    expect(screen.queryByText(/scenarioTemplates\.readiness\.trial_completed$/)).toBeNull();
    expect(readiness.get).not.toHaveBeenCalled();
  });

  it("links the approval center only for roles that may actually submit a request", () => {
    access.map = { "dataset:read": true, "chat:manage": false, "scenario-template:read": true, "approval:execute": true };
    permissions.value = { approvalsEnabled: true };

    renderEmptyState();

    const request = screen.getByText("conversationCenter.requestAssistant.request").closest("a");
    expect(request).toHaveAttribute("href", "/approvals/create");
    expect(screen.queryByText("conversationCenter.requestAssistant.copyRequest")).toBeNull();
  });

  it("tells a viewer without the approval center why the request path is missing", () => {
    access.map = { "dataset:read": true, "chat:manage": false, "scenario-template:read": false, "approval:execute": false };
    permissions.value = { approvalsEnabled: false };

    renderEmptyState();

    expect(screen.getByText("conversationCenter.requestAssistant.unavailable")).toBeInTheDocument();
    expect(screen.queryByText("conversationCenter.requestAssistant.request")).toBeNull();
    expect(screen.queryByText("conversationCenter.requestAssistant.copyRequest")).toBeNull();
  });

  it("gives an operator the full four-step onboarding with readiness progress", async () => {
    access.map = { "dataset:read": true, "chat:manage": true, "scenario-template:read": true };

    renderEmptyState();

    expect(screen.queryByText("conversationCenter.requestAssistant.title")).toBeNull();
    for (const key of ["dataset_ready", "assistant_ready", "trial_completed", "first_cited_answer"]) {
      expect(screen.getByText(new RegExp(`scenarioTemplates\\.readiness\\.${key}$`))).toBeInTheDocument();
    }
    expect(await screen.findByText(/scenarioTemplates\.readiness\.progress/)).toBeInTheDocument();
    expect(readiness.get).toHaveBeenCalledWith("chat-1");
  });

  it("renders nothing permission-specific while the permission set is still resolving", () => {
    access.map = {};
    permissions.value = { approvalsEnabled: true };

    renderEmptyState();

    expect(screen.queryByText("conversationCenter.requestAssistant.title")).toBeNull();
    expect(screen.queryByText(/scenarioTemplates\.readiness\.assistant_ready$/)).toBeNull();
    expect(screen.getByText("conversationCenter.example_1")).toBeInTheDocument();
  });
});
