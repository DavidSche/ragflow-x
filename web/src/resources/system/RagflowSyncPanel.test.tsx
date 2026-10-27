// ScenarioID: SC-SYNC-001
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RagflowSyncPanel } from "./RagflowSyncPanel";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {
    displayMessage: string;
    constructor(message: string) {
      super(message);
      this.displayMessage = message;
    }
  },
  api,
  unwrap: async (request: Promise<unknown>) => request,
}));

vi.mock("ra-core", () => ({
  useNotify: () => api.notify,
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/components/ui/select", () => ({
  Select: ({ children, value, disabled, onValueChange }: { children: React.ReactNode; value: string; disabled?: boolean; onValueChange?: (value: string) => void }) => (
    <select value={value} disabled={disabled} onChange={(event) => onValueChange?.(event.target.value)}>{children}</select>
  ),
  SelectTrigger: () => null,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SelectItem: ({ value, children }: { value: string; children: React.ReactNode }) => <option value={value}>{children}</option>,
}));

const setting = {
  enabled: true,
  scheduled_reconcile_enabled: false,
  resource_types: ["dataset"],
  interval_seconds: 900,
  batch_size: 100,
  max_resources: 5000,
  deletion_confirmations: 3,
  default_target_tenant_id: "tenant-1",
  default_owner_id: "",
};

const resource = {
  id: "binding-1",
  resource_type: "dataset",
  external_tenant_id: "__GLOBAL__",
  external_id: "rag-1",
  local_id: "local-1",
  tenant_id: "tenant-1",
  binding_lifecycle: "active",
  governance_state: "normal",
  conflict_type: "content",
};

describe("RagflowSyncPanel governance controls", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.endsWith("/settings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: setting } });
      }
      if (url.endsWith("/mappings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: [] } });
      }
      if (url.endsWith("/runs?page=1&page_size=10")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "run-1", trigger_type: "manual_import", status: "planned", scan_consistency: "page_scan_approximate", deletion_safe: false, plan_summary: "{}", result_summary: "{}", created_at: "2026-01-01" }] } } });
      }
      if (url.endsWith("/resources?page=1&page_size=20")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [resource] } } });
      }
      if (url.includes("/runs/run-1/items")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{
          id: "item-1", resource_type: "dataset", external_tenant_id: "__GLOBAL__", external_id: "rag-1", local_id: "local-1",
          tenant_id: "tenant-1", owner_id: "",
          action: "conflict", status: "pending", conflict_type: "content", error: "",
          upstream_current_hash: "u2", upstream_last_synced_hash: "u1", local_current_hash: "l2", local_last_synced_hash: "l1",
          payload_diff: "{\"action\":\"conflict\",\"hashes\":{\"upstream_current\":\"u2\"},\"upstream_payload\":\"old\"}",
        }, {
          id: "item-2", resource_type: "memory", external_tenant_id: "__GLOBAL__", external_id: "memory-1", local_id: "",
          tenant_id: "", owner_id: "",
          action: "create", status: "pending", conflict_type: "none", error: "",
          upstream_current_hash: "m1", upstream_last_synced_hash: "", local_current_hash: "", local_last_synced_hash: "",
          payload_diff: "{}",
        }] } } });
      }
      if (url.includes("/relink/tenant-options")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "tenant-1", name: "Tenant One", type: "workspace", status: "active" }] } } });
      }
      if (url.includes("/relink/resource-options")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ tenant_id: "tenant-1", local_id: "local-2", name: "Relink Target" }] } } });
      }
      if (url.includes("/versions")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "version-1", version: 2, upstream_hash: "u2", source_credential_version: "ragflow_global_v1", sync_run_id: "run-1", created_at: "2026-01-01" }] } } });
      }
      if (url.startsWith("/users?")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "user-1", username: "Owner", tenant_id: "tenant-1", status: "active" }] } } });
      }
      throw new Error(`unexpected GET ${url}`);
    });
  });

  it("loads run item diffs and resolves a content conflict", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: resource } });
    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    fireEvent.click(await screen.findByRole("button", { name: "system.ragflow_sync_view_items" }));
    expect(await screen.findByText(/"hashes"/)).toBeInTheDocument();
    expect(screen.queryByText(/"old"/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_use_upstream" }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/system/ragflow/sync/resources/dataset/__GLOBAL__/rag-1/conflicts/resolve",
      { item_id: "item-1", resolution: "use_upstream" },
    ));
  });

  it("loads binding versions and relinks with explicit target", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: resource } });
    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_resources" }));
    await waitFor(() => expect(container.querySelectorAll("select")[1]).not.toBeDisabled());
    fireEvent.click(await screen.findByRole("button", { name: "system.ragflow_sync_versions" }));
    expect(await screen.findByText("ragflow_global_v1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_select_relink" }));
    await waitFor(() => expect(container.querySelectorAll("select")[2]).not.toBeDisabled());
    fireEvent.change(container.querySelectorAll("select")[2], { target: { value: "local-2" } });
    const relinkButton = screen.getByRole("button", { name: "system.ragflow_sync_relink" });
    await waitFor(() => expect(relinkButton).not.toBeDisabled());
    fireEvent.click(relinkButton);
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/system/ragflow/sync/resources/dataset/__GLOBAL__/rag-1/relink",
      { scope: "current", tenant_id: "tenant-1", local_id: "local-2" },
    ));
  });

  it("imports a planned run explicitly", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: { updated: 1 } } });
    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    fireEvent.click(await screen.findByRole("button", { name: "system.ragflow_sync_view_items" }));
    const checkbox = await screen.findByRole("checkbox", { name: /system.ragflow_sync_select memory\/memory-1/ });
    fireEvent.click(checkbox);
    await waitFor(() => expect(checkbox).toBeChecked());
    fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_import" }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/system/ragflow/sync/runs/run-1/import",
      { async: true },
    ));
  });

  it("assigns selected create items", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: { updated: 1 } } });
    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    fireEvent.click(await screen.findByRole("button", { name: "system.ragflow_sync_view_items" }));
    const checkbox = await screen.findByRole("checkbox", { name: /system.ragflow_sync_select memory\/memory-1/ });
    fireEvent.click(checkbox);
    await waitFor(() => expect(checkbox).toBeChecked());
    await waitFor(() => expect(screen.getByText("system.ragflow_sync_selected")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("button", { name: "system.ragflow_sync_assign" })).not.toBeDisabled());
    fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_assign" }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/system/ragflow/sync/runs/run-1/assign",
      { item_ids: ["item-2"], tenant_id: "tenant-1", owner_id: undefined },
    ));
  });

  it("blocks scan and reconcile while a planned run awaits import", async () => {
    render(<RagflowSyncPanel />);
    expect(await screen.findByText(/system.ragflow_sync_active_run/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "system.ragflow_sync_scan" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "system.ragflow_sync_reconcile" })[0]).toBeDisabled();
  });

  it("exposes the first-import wizard workflow", async () => {
    render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_onboarding" }));
    expect(screen.getByRole("region", { name: "system.ragflow_sync_wizard_title" })).toBeInTheDocument();
    expect(screen.getByText("system.ragflow_sync_wizard_step3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "system.ragflow_sync_tab_overview" }));
    expect(screen.queryByText("system.ragflow_sync_wizard_step3")).not.toBeInTheDocument();
  });

  it("selects a workspace for the default sync mapping", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: { external_tenant_id: "__GLOBAL__", target_tenant_id: "tenant-1" } } });
    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_onboarding" }));
    await waitFor(() => expect(container.querySelector("select")?.options).toHaveLength(1));
    const workspaceSelect = container.querySelector("select") as HTMLSelectElement;
    expect(workspaceSelect).not.toBeDisabled();
    fireEvent.change(workspaceSelect, { target: { value: "tenant-1" } });
    const saveMappingButton = screen.getByRole("button", { name: "system.ragflow_sync_save_mapping" });
    await waitFor(() => expect(saveMappingButton).not.toBeDisabled());
    fireEvent.click(saveMappingButton);
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/system/ragflow/sync/mappings", {
      external_tenant_id: "__GLOBAL__",
      target_tenant_id: "tenant-1",
    }));
  });

  it("auto-selects a planned run and explains an empty plan", async () => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.endsWith("/settings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: setting } });
      }
      if (url.endsWith("/mappings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: [] } });
      }
      if (url.endsWith("/runs?page=1&page_size=10")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "empty-run", trigger_type: "manual_import", status: "planned", scan_consistency: "page_scan_approximate", deletion_safe: false, plan_summary: "{}", result_summary: "{}", error: "", created_at: "2026-01-01" }] } } });
      }
      if (url.endsWith("/resources?page=1&page_size=20")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [] } } });
      }
      if (url.includes("/runs/empty-run/items")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [], total: 0 } } });
      }
      throw new Error(`unexpected GET ${url}`);
    });

    render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    expect(await screen.findByText("system.ragflow_sync_no_plan_items")).toBeInTheDocument();
    expect(screen.getByText("system.ragflow_sync_selected_run")).toBeInTheDocument();
    expect(api.get).toHaveBeenCalledWith("/system/ragflow/sync/runs/empty-run/items?page=1&page_size=100");
  });

  it("flags a planned run whose summary and details disagree", async () => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.endsWith("/settings")) return Promise.resolve({ status: 200, data: { code: 0, data: setting } });
      if (url.endsWith("/mappings")) return Promise.resolve({ status: 200, data: { code: 0, data: [] } });
      if (url.endsWith("/runs?page=1&page_size=10")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "mismatch-run", trigger_type: "manual_import", status: "planned", scan_consistency: "page_scan_approximate", deletion_safe: false, plan_summary: JSON.stringify({ dataset: { mapping: 2, conflict: 2 } }), result_summary: "{}", error: "", created_at: "2026-01-01" }] } } });
      }
      if (url.endsWith("/resources?page=1&page_size=20")) return Promise.resolve({ status: 200, data: { code: 0, data: { items: [] } } });
      if (url.includes("/runs/mismatch-run/items")) return Promise.resolve({ status: 200, data: { code: 0, data: { items: [], total: 0 } } });
      throw new Error(`unexpected GET ${url}`);
    });

    render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    expect(await screen.findByText("system.ragflow_sync_plan_item_mismatch")).toBeInTheDocument();
    expect(screen.getByText(/dataset · mapping · 2/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "system.ragflow_sync_import" })).toBeDisabled();
  });

  it("renders run items with missing sync hashes without crashing", async () => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.endsWith("/settings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: setting } });
      }
      if (url.endsWith("/mappings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: [] } });
      }
      if (url.endsWith("/runs?page=1&page_size=10")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{ id: "partial-run", trigger_type: "manual_import", status: "planned", scan_consistency: "page_scan_approximate", deletion_safe: false, plan_summary: "{}", result_summary: "{}", created_at: "2026-01-01" }] } } });
      }
      if (url.endsWith("/resources?page=1&page_size=20")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [] } } });
      }
      if (url.includes("/runs/partial-run/items")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { items: [{
          id: "partial-item", resource_type: "dataset", external_tenant_id: "__GLOBAL__", external_id: "rag-1", local_id: "partial-local",
          tenant_id: "tenant-1", owner_id: "", action: "create", status: "pending", conflict_type: "", error: "",
          upstream_current_hash: "upstream-current", local_current_hash: "local-current", payload_diff: "{}",
        }], total: 1 } } });
      }
      throw new Error(`unexpected GET ${url}`);
    });

    const { container } = render(<RagflowSyncPanel />);
    fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_runs" }));
    expect(await screen.findByText("dataset · create")).toBeInTheDocument();
    expect(container.textContent).toContain("→ upstream");
    expect(container.textContent).toContain("→ local-cu");
  });
});

it("submits the independent scheduled reconciliation switch", async () => {
  api.patch.mockResolvedValue({ status: 200, data: { code: 0, data: setting } });
  render(<RagflowSyncPanel />);
  fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_settings" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: "system.ragflow_sync_scheduled_reconcile_enabled" }));
  fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_save" }));
  await waitFor(() => expect(api.patch).toHaveBeenCalledWith("/system/ragflow/sync/settings", {
    ...setting,
    scheduled_reconcile_enabled: true,
  }));
});

it("saves pending sync settings before scanning", async () => {
  vi.clearAllMocks();
  const disabledSetting = { ...setting, enabled: false };
  api.get.mockImplementation((url: string) => {
    if (url.endsWith("/settings")) {
      return Promise.resolve({ status: 200, data: { code: 0, data: disabledSetting } });
    }
    if (url.endsWith("/mappings")) {
      return Promise.resolve({ status: 200, data: { code: 0, data: [] } });
    }
    if (url.endsWith("/runs?page=1&page_size=10")) {
      return Promise.resolve({ status: 200, data: { code: 0, data: { items: [] } } });
    }
    if (url.endsWith("/resources?page=1&page_size=20")) {
      return Promise.resolve({ status: 200, data: { code: 0, data: { items: [] } } });
    }
    throw new Error(`unexpected GET ${url}`);
  });
  api.patch.mockResolvedValue({ status: 200, data: { code: 0, data: { ...disabledSetting, enabled: true } } });
  api.post.mockResolvedValue({ status: 200, data: { code: 0, data: { id: "run-1" } } });

  render(<RagflowSyncPanel />);
  fireEvent.click(await screen.findByRole("tab", { name: "system.ragflow_sync_tab_settings" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: "system.ragflow_sync_enabled" }));
  fireEvent.click(screen.getByRole("button", { name: "system.ragflow_sync_scan" }));

  await waitFor(() => expect(api.post).toHaveBeenCalledWith("/system/ragflow/sync/scan", { resource_types: ["dataset"] }));
  expect(api.patch).toHaveBeenCalledWith("/system/ragflow/sync/settings", { ...setting, enabled: true });
  expect(api.patch.mock.invocationCallOrder[0]).toBeLessThan(api.post.mock.invocationCallOrder[0]);
});


