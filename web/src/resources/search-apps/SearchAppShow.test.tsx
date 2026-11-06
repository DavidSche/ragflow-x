import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SearchAppShow } from "./SearchAppShow";

vi.mock("react-router-dom", () => ({
  useParams: () => ({ id: "sa-1" }),
}));

vi.mock("ra-core", () => ({
  useGetList: () => ({ data: [] }),
  useNotify: () => vi.fn(),
  useTranslate: () => (key: string) => key,
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: { get: vi.fn(), put: vi.fn() },
}));

import { api } from "../../lib/api";

// Isolate the config form: stub heavy siblings / leaf primitives so assertions
// target the search-app model parameter panel itself.
vi.mock("../../components/admin", () => ({
  Show: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../workbench/ConversationShell", () => ({ ConversationShell: () => null }));
vi.mock("../workbench/conversation-export", () => ({ downloadConversation: () => {} }));
vi.mock("../workbench/ConversationExportMenu", () => ({ ConversationExportMenu: () => null }));
vi.mock("../workbench/PromptPresetPicker", () => ({ PromptPresetPicker: () => null }));
vi.mock("@/components/ui/button", () => ({
  Button: ({ children, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement>) => <button type="button" {...props}>{children}</button>,
}));
vi.mock("@/components/ui/input", () => ({
  Input: (props: React.InputHTMLAttributes<HTMLInputElement>) => <input {...props} />,
}));
vi.mock("@/components/ui/label", () => ({
  Label: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));

// Loaded engine config: temperature + top_p + max_tokens all pre-set so the
// round-trip test proves a plain save does NOT drop the extra knobs.
const liveConfig = {
  data: {
    code: 0,
    data: {
      name: "my-search",
      search_config: {
        kb_ids: [],
        doc_ids: [],
        similarity_threshold: 0.2,
        vector_similarity_weight: 0.3,
        top_k: 8,
        chat_id: "m1",
        use_rerank: false,
        rerank_id: "",
        summary: false,
        related_search: false,
        query_mindmap: false,
        use_kg: false,
        reference_metadata: { include: false, fields: [] },
        llm_setting: { llm_id: "m1", temperature: 0, top_p: 0.1, max_tokens: 800 },
      },
    },
  },
} as never;
const modelOptions = { data: { code: 0, data: { chat: [], rerank: [], defaults: [] } } } as never;

const setup = async () => {
  vi.mocked(api.get).mockReset();
  vi.mocked(api.put).mockReset();
  vi.mocked(api.get).mockImplementation(async (url: string) => {
    if (url.endsWith("/config")) return liveConfig;
    return modelOptions;
  });
  vi.mocked(api.put).mockResolvedValue({ data: { code: 0, data: {} } } as never);
  render(<SearchAppShow />);
  await waitFor(() => expect(screen.getByText("searchApps.llm_parameters")).toBeInTheDocument());
};

describe("<SearchAppShow /> model parameters", () => {
  it("renders an independent parameter panel with temperature / top_p / max_tokens", async () => {
    await setup();
    // Panel heading + hints
    expect(screen.getByText("searchApps.llm_parameters")).toBeInTheDocument();
    // Model-parameter sliders render first (temperature, top_p), then the 3 retrieval sliders
    const sliders = screen.getAllByRole("slider");
    expect(sliders).toHaveLength(5);
    expect(sliders[0]).toHaveValue("0"); // temperature
    expect(sliders[1]).toHaveValue("0.1"); // top_p
    // max_tokens number field is pre-filled from the live engine config
    expect(screen.getByDisplayValue("800")).toBeInTheDocument();
  });

  it("saving without edits preserves top_p and max_tokens (no silent data loss)", async () => {
    await setup();
    await userEvent.click(screen.getByText("ra.action.save"));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    const [url, payload] = vi.mocked(api.put).mock.calls[0] as [string, { search_config: { llm_setting: Record<string, unknown> } }];
    expect(url).toBe("/search-apps/sa-1");
    expect(payload.search_config.llm_setting).toEqual({ llm_id: "m1", temperature: 0, top_p: 0.1, max_tokens: 800 });
  });

  it("surfaces the deterministic default signature for knobs the engine left unset", async () => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url.endsWith("/config")) {
        return {
          data: {
            code: 0,
            data: {
              name: "minimal",
              search_config: {
                kb_ids: [], doc_ids: [], similarity_threshold: 0.2, vector_similarity_weight: 0.3,
                top_k: 8, chat_id: "m1", use_rerank: false, rerank_id: "", summary: false,
                related_search: false, query_mindmap: false, use_kg: false,
                reference_metadata: { include: false, fields: [] },
                llm_setting: { llm_id: "m1", temperature: 0.5 },
              },
            },
          },
        } as never;
      }
      return modelOptions;
    });
    vi.mocked(api.put).mockResolvedValue({ data: { code: 0, data: {} } } as never);
    // Isolate from prior tests: only look at put calls made during THIS test.
    vi.mocked(api.put).mockClear();
    render(<SearchAppShow />);
    await waitFor(() => expect(screen.getByText("searchApps.llm_parameters")).toBeInTheDocument());

    await userEvent.click(screen.getByText("ra.action.save"));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    const payload = vi.mocked(api.put).mock.calls[0][1] as { search_config: { llm_setting: Record<string, unknown> } };
    // default-first: user-set temperature is honoured, unset top_p/max_tokens become
    // the E-proven deterministic signature (0.1 / 800) instead of engine defaults.
    expect(payload.search_config.llm_setting).toEqual({ llm_id: "m1", temperature: 0.5, top_p: 0.1, max_tokens: 800 });
  });

  it("editing the sliders and max_tokens is reflected in the save payload", async () => {
    await setup();
    const sliders = screen.getAllByRole("slider");
    const temperature = sliders[0];
    const topP = sliders[1];
    const maxTokens = screen.getByDisplayValue("800");

    fireEvent.change(temperature, { target: { value: "0.25" } });
    fireEvent.change(topP, { target: { value: "0.9" } });
    await userEvent.clear(maxTokens);
    await userEvent.type(maxTokens, "2048");

    await userEvent.click(screen.getByText("ra.action.save"));
    await waitFor(() => expect(api.put).toHaveBeenCalled());
    const payload = vi.mocked(api.put).mock.calls[0][1] as { search_config: { llm_setting: Record<string, unknown> } };
    expect(payload.search_config.llm_setting).toEqual({ llm_id: "m1", temperature: 0.25, top_p: 0.9, max_tokens: 2048 });
  });

  it("blocks saving invalid max_tokens with an error instead of a silent downgrade", async () => {
    await setup();
    const maxTokens = screen.getByDisplayValue("800");
    await userEvent.clear(maxTokens);
    await userEvent.type(maxTokens, "0");
    // invalid input surfaces an inline error + red (aria-invalid) state and a save is refused.
    expect(screen.getByText("searchApps.llm_max_tokens_invalid")).toBeInTheDocument();
    await userEvent.click(screen.getByText("ra.action.save"));
    await waitFor(() => expect(api.put).not.toHaveBeenCalled());
  });

  it("surfaces a load-failure state with a working retry instead of an empty form", async () => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.get).mockRejectedValueOnce(new Error("boom"));
    vi.mocked(api.get).mockImplementation(async (url: string) => {
      if (url.endsWith("/config")) return liveConfig;
      return modelOptions;
    });
    vi.mocked(api.put).mockResolvedValue({ data: { code: 0, data: {} } } as never);

    render(<SearchAppShow />);
    await waitFor(() =>
      expect(screen.getByText("searchApps.config_load_failed")).toBeInTheDocument(),
    );
    // error state must not silently present an empty config as if it were live.
    expect(api.put).not.toHaveBeenCalled();

    await userEvent.click(screen.getByText("searchApps.retry"));
    await waitFor(() =>
      expect(screen.getByText("searchApps.llm_parameters")).toBeInTheDocument(),
    );
  });
});
