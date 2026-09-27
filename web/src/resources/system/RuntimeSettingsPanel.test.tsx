import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RuntimeSettingsPanel } from "./RuntimeSettingsPanel";

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
  unwrap: async <T,>(request: Promise<{ data: { data: T } }>) => (await request).data.data,
}));

vi.mock("ra-core", () => ({
  useNotify: () => api.notify,
  useTranslate: () => (key: string) => key,
  useLocaleState: () => ["zh", vi.fn()],
}));

vi.mock("../../lib/seal", () => ({
  sealRSAOAEPSHA256: async (_pem: string, plaintext: string) => `sealed:${plaintext}`,
}));

const settings = {
  revision: 4,
  desired_revision_id: "revision-4",
  deployment_effective_state: "consistent",
  groups: {
    security: {
      allowed_origins: {
        effective: ["https://old.example.com"],
        source: "yaml_env_db",
        editable: true,
        database_override: true,
        restart_required: false,
        risk_level: "high",
        impact: "expands cross-origin browser access",
        effective_mode: "hot_reload",
        type: "origin_list",
      },
    },
    alerting: {
      webhooks: {
        effective: [{ name: "ops", url: "https://alerts.example.com/hook", enabled: true }],
        source: "yaml_env_db",
        editable: true,
        database_override: true,
        restart_required: false,
        risk_level: "high",
        impact: "sends alerts to external systems",
        effective_mode: "hot_reload",
        type: "webhook_list",
      },
    },
    observability: {
      sample_ratio: {
        effective: 0.5,
        source: "yaml_env_db",
        editable: true,
        database_override: true,
        restart_required: false,
        risk_level: "low",
        impact: "changes trace sampling",
        effective_mode: "hot_reload",
        type: "number",
        min: 0,
        max: 1,
      },
      otel_trace_ui_url_template: {
        effective: "https://grafana.example.com/trace/{trace_id}?span={span_id}",
        source: "yaml_env",
        editable: false,
        database_override: false,
        restart_required: true,
        risk_level: "low",
        impact: "changes trace deep-link target persisted in evidence pointers",
        effective_mode: "deployment_controlled",
        type: "string",
      },
    },
  },
  runtime_instances: [
    {
      runtime_instance_id: "runtime-1",
      apply_status: "applied",
      apply_error: "",
      last_seen_at: "2026-09-08T00:00:00Z",
      heartbeat_state: "stale",
    },
  ],
  secret_references: {
    "observability.metrics_token": { id: "secret-id", version: 3 },
  },
};

describe("RuntimeSettingsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.endsWith("/system/settings")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: settings } });
      }
      if (url.includes("/revisions")) {
        return Promise.resolve({
          status: 200,
          data: { code: 0, data: { items: [{ id: "revision-3", revision: 3, revision_status: "created", created_by: "admin", created_at: "", note: "" }] } },
        });
      }
      if (url.endsWith("/public-key")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: { public_key: "PUBLIC KEY" } } });
      }
      throw new Error(`unexpected GET ${url}`);
    });
  });

  it("submits list values by declared type and rotates secrets through the credential API", async () => {
    api.patch.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });
    render(<RuntimeSettingsPanel />);

    await screen.findByText("security.allowed_origins");
    expect(screen.getByText("扩大浏览器跨源访问范围")).toHaveClass("text-muted-foreground");
    expect(screen.getByText("将告警发送到外部系统")).not.toHaveClass("text-red-600");

    await screen.findByText("security.allowed_origins");
    expect(screen.getByText(/runtime-1 · applied · system\.heartbeat_stale/)).toBeInTheDocument();

    // Deployment-controlled trace UI template (doc/104 §15): visible read-only
    // with a mono input and a deployment-control badge, never editable.
    const template = screen.getByLabelText("observability.otel_trace_ui_url_template") as HTMLInputElement;
    expect(template).toHaveValue("https://grafana.example.com/trace/{trace_id}?span={span_id}");
    expect(template).toHaveAttribute("readonly");
    expect(screen.getByText(/system\.settings_deployment_controlled/)).toBeInTheDocument();
    const origins = screen.getByText("security.allowed_origins").closest("label")?.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(origins, { target: { value: "https://new.example.com" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "system.settings_confirm_high_risk" }));
    fireEvent.click(screen.getByRole("button", { name: "system.settings_save" }));
    await waitFor(() => expect(api.patch).toHaveBeenCalledWith("/system/settings", expect.objectContaining({
      expected_revision: 4,
      changes: { "security.allowed_origins": ["https://new.example.com"] },
    })));

    const ratio = screen.getByLabelText("observability.sample_ratio") as HTMLInputElement;
    expect(screen.getByRole("slider", { name: /observability\.sample_ratio/ })).toHaveValue("0.5");
    fireEvent.change(ratio, { target: { value: "2" } });
    expect(screen.getByText(/system\.settings_number_out_of_range/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "system.settings_save" })).toBeDisabled();
    fireEvent.change(screen.getByRole("slider", { name: /observability\.sample_ratio/ }), { target: { value: "0.8" } });
    expect(screen.queryByText(/system\.settings_number_out_of_range/)).not.toBeInTheDocument();

    const secret = screen.getByText("observability.metrics_token").closest("label")?.querySelector("input") as HTMLInputElement;
    fireEvent.change(secret, { target: { value: "next-secret" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "system.settings_confirm_high_risk" }));
    fireEvent.click(screen.getAllByRole("button", { name: "system.settings_secret_save" })[0]);
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(
      "/system/settings/secrets/observability.metrics_token",
      expect.objectContaining({ secret: "sealed:next-secret" }),
    ));
    expect(screen.getByText("runtime.report_token")).toBeInTheDocument();
  });

  it("requires an explicit rollback reason before rolling back", async () => {
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });
    render(<RuntimeSettingsPanel />);

    await screen.findByText(/#3 · created · admin/);
    fireEvent.click(screen.getByRole("checkbox", { name: "system.settings_confirm_high_risk" }));
    fireEvent.click(screen.getByRole("button", { name: "system.settings_rollback" }));
    expect(api.post).not.toHaveBeenCalled();
    expect(api.notify).toHaveBeenCalledWith("system.settings_rollback_note_invalid", expect.anything());

    fireEvent.change(screen.getByLabelText("system.settings_rollback_note"), {
      target: { value: "restore approved metrics token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "system.settings_rollback" }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/system/settings/revisions/revision-3/rollback", {
      confirmed: true,
      note: "restore approved metrics token",
    }));
  });
});
