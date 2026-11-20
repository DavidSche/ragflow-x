import { describe, expect, it } from "vitest";
import { graphToDsl, newNodeId, resolveComponentName } from "./dslUtils";

/**
 * The engine resolves `{id@variable}` only for ids matching `[a-zA-Z:0-9_]+`.
 * A hyphenated id is not an error there: the reference silently stays literal
 * text in the answer, which reads like a model failure rather than an id bug.
 * These tests pin the editor half of that contract; the Go boundary
 * (internal/provider/ragflow/agent_dsl_normalize.go) is the other half and
 * covers DSLs that arrive without passing through this editor.
 */
describe("newNodeId", () => {
  const LEGAL = /^[A-Za-z0-9_:]+$/;

  it("emits ids the engine can reference", () => {
    const ids = ["node", "start"].flatMap((prefix) =>
      Array.from({ length: 25 }, () => newNodeId(prefix)),
    );
    for (const id of ids) {
      expect(id).toMatch(LEGAL);
    }
  });

  it("emits unique ids", () => {
    const ids = Array.from({ length: 200 }, () => newNodeId("node"));
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("keeps the prefix readable", () => {
    expect(newNodeId("node").startsWith("node_")).toBe(true);
    expect(newNodeId("start").startsWith("start_")).toBe(true);
  });
});

describe("resolveComponentName", () => {
  it("keeps the friendly label so the editor round-trips", () => {
    // Translation to the engine's registry (Generate → llm, Knowledge →
    // retrieval, Answer → message) happens once, on the Go engine boundary.
    for (const label of ["Begin", "Knowledge", "Generate", "Answer"]) {
      expect(resolveComponentName(label)).toBe(label);
    }
  });
});

describe("graphToDsl", () => {
  it("keeps every node id referencable and leaves the prompt names to the boundary", () => {
    const dsl = graphToDsl(
      [
        {
          id: newNodeId("node"),
          type: "agentNode",
          position: { x: 0, y: 0 },
          data: {
            label: "Generate",
            form: { llm_id: "m1", system: "s", prompt: "p {sys.query}" },
          },
        } as any,
      ],
      [],
      { components: {}, graph: { nodes: [], edges: [] } },
    );

    const ids = [
      ...Object.keys(dsl.components),
      ...dsl.graph.nodes.map((n: any) => n.id),
    ];
    for (const id of ids) expect(id).toMatch(/^[A-Za-z0-9_:]+$/);
    // The canvas form binds `system`/`prompt`; renaming them here would break
    // the property panel. The boundary renames them on the wire.
    const component = Object.values(dsl.components)[0] as any;
    expect(component.obj.params.system).toBe("s");
    expect(component.obj.params.prompt).toBe("p {sys.query}");
  });
});
