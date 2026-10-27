// DSL diff utility — compares two RAGFlow agent DSLs and produces a
// structured summary of added/removed/modified nodes, edges, and components.

export interface DiffNode {
  id: string;
  label: string;
  change: "added" | "removed" | "modified";
  details?: string[];
}

export interface DiffEdge {
  id: string;
  change: "added" | "removed" | "modified";
  label?: string;
}

export interface DiffComponent {
  id: string;
  componentName: string;
  change: "added" | "removed" | "modified";
  details?: string[];
}

export interface DslDiff {
  nodes: DiffNode[];
  edges: DiffEdge[];
  components: DiffComponent[];
  stats: {
    nodesAdded: number;
    nodesRemoved: number;
    nodesModified: number;
    edgesAdded: number;
    edgesRemoved: number;
    edgesModified: number;
    componentsAdded: number;
    componentsRemoved: number;
    componentsModified: number;
  };
  isEmpty: boolean;
}

function getNodeLabel(node: any): string {
  return node?.data?.label ?? node?.label ?? "Unknown";
}

function shallowEqual(a: any, b: any): boolean {
  if (a === b) return true;
  if (a == null || b == null) return false;
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  for (const k of keysA) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

function deepEqual(a: any, b: any): boolean {
  if (a === b) return true;
  if (a == null || b == null) return false;
  if (typeof a !== typeof b) return false;
  if (typeof a !== "object") return a === b;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  for (const k of keysA) {
    if (!deepEqual(a[k], b[k])) return false;
  }
  return true;
}

function getNodeChanges(oldNode: any, newNode: any): string[] {
  const changes: string[] = [];
  // Compare form fields (the main payload)
  const oldForm = oldNode?.data?.form ?? {};
  const newForm = newNode?.data?.form ?? {};
  if (!deepEqual(oldForm, newForm)) {
    // Find which fields changed
    const allKeys = new Set([...Object.keys(oldForm), ...Object.keys(newForm)]);
    for (const key of allKeys) {
      if (!deepEqual(oldForm[key], newForm[key])) {
        changes.push(`form.${key}`);
      }
    }
  }
  // Compare title
  const oldTitle = oldNode?.data?.title;
  const newTitle = newNode?.data?.title;
  if (oldTitle !== newTitle) {
    changes.push(`title: "${oldTitle || ""}" → "${newTitle || ""}"`);
  }
  // Compare position
  const oldPos = oldNode?.position;
  const newPos = newNode?.position;
  if (oldPos && newPos && (oldPos.x !== newPos.x || oldPos.y !== newPos.y)) {
    changes.push("position moved");
  }
  return changes;
}

function getComponentChanges(oldComp: any, newComp: any): string[] {
  const changes: string[] = [];
  const oldObj = oldComp?.obj ?? {};
  const newObj = newComp?.obj ?? {};

  // Compare component_name
  if (oldObj.component_name !== newObj.component_name) {
    changes.push(`component_name: "${oldObj.component_name}" → "${newObj.component_name}"`);
  }

  // Compare params
  if (!deepEqual(oldObj.params, newObj.params)) {
    const oldParams = oldObj.params ?? {};
    const newParams = newObj.params ?? {};
    const allKeys = new Set([...Object.keys(oldParams), ...Object.keys(newParams)]);
    for (const key of allKeys) {
      if (!deepEqual(oldParams[key], newParams[key])) {
        changes.push(`params.${key}`);
      }
    }
  }

  // Compare downstream/upstream
  const oldDown = JSON.stringify(oldComp?.downstream ?? []);
  const newDown = JSON.stringify(newComp?.downstream ?? []);
  if (oldDown !== newDown) {
    changes.push("downstream changed");
  }
  const oldUp = JSON.stringify(oldComp?.upstream ?? []);
  const newUp = JSON.stringify(newComp?.upstream ?? []);
  if (oldUp !== newUp) {
    changes.push("upstream changed");
  }

  return changes;
}

/**
 * Compute a structured diff between two RAGFlow agent DSLs.
 * `oldDsl` is the version's DSL; `newDsl` is the current (unsaved) DSL.
 * The diff answers: "what changed from the version to the current state?"
 */
export function computeDslDiff(oldDsl: Record<string, any>, newDsl: Record<string, any>): DslDiff {
  const oldNodes: any[] = Array.isArray(oldDsl?.graph?.nodes) ? oldDsl.graph.nodes : [];
  const newNodes: any[] = Array.isArray(newDsl?.graph?.nodes) ? newDsl.graph.nodes : [];
  const oldEdges: any[] = Array.isArray(oldDsl?.graph?.edges) ? oldDsl.graph.edges : [];
  const newEdges: any[] = Array.isArray(newDsl?.graph?.edges) ? newDsl.graph.edges : [];
  const oldComps: Record<string, any> = oldDsl?.components ?? {};
  const newComps: Record<string, any> = newDsl?.components ?? {};

  const oldNodeMap = new Map(oldNodes.map((n: any) => [String(n.id), n]));
  const newNodeMap = new Map(newNodes.map((n: any) => [String(n.id), n]));
  const oldEdgeMap = new Map(oldEdges.map((e: any) => [String(e.id), e]));
  const newEdgeMap = new Map(newEdges.map((e: any) => [String(e.id), e]));

  // --- Nodes ---
  const diffNodes: DiffNode[] = [];
  let nodesAdded = 0, nodesRemoved = 0, nodesModified = 0;

  for (const [id, newNode] of newNodeMap) {
    const oldNode = oldNodeMap.get(id);
    if (!oldNode) {
      diffNodes.push({ id, label: getNodeLabel(newNode), change: "added" });
      nodesAdded++;
    } else {
      const details = getNodeChanges(oldNode, newNode);
      if (details.length > 0) {
        diffNodes.push({ id, label: getNodeLabel(newNode), change: "modified", details });
        nodesModified++;
      }
    }
  }
  for (const [id, oldNode] of oldNodeMap) {
    if (!newNodeMap.has(id)) {
      diffNodes.push({ id, label: getNodeLabel(oldNode), change: "removed" });
      nodesRemoved++;
    }
  }

  // --- Edges ---
  const diffEdges: DiffEdge[] = [];
  let edgesAdded = 0, edgesRemoved = 0, edgesModified = 0;

  for (const [id, newEdge] of newEdgeMap) {
    const oldEdge = oldEdgeMap.get(id);
    if (!oldEdge) {
      diffEdges.push({ id, change: "added" });
      edgesAdded++;
    } else if (
      oldEdge.source !== newEdge.source ||
      oldEdge.target !== newEdge.target ||
      oldEdge.sourceHandle !== newEdge.sourceHandle ||
      oldEdge.targetHandle !== newEdge.targetHandle
    ) {
      diffEdges.push({ id, change: "modified" });
      edgesModified++;
    }
  }
  for (const [id] of oldEdgeMap) {
    if (!newEdgeMap.has(id)) {
      diffEdges.push({ id, change: "removed" });
      edgesRemoved++;
    }
  }

  // --- Components ---
  const diffComps: DiffComponent[] = [];
  let compsAdded = 0, compsRemoved = 0, compsModified = 0;

  for (const [id, newComp] of Object.entries(newComps)) {
    const oldComp = oldComps[id];
    if (!oldComp) {
      diffComps.push({
        id,
        componentName: newComp?.obj?.component_name ?? "Unknown",
        change: "added",
      });
      compsAdded++;
    } else {
      const details = getComponentChanges(oldComp, newComp);
      if (details.length > 0) {
        diffComps.push({
          id,
          componentName: newComp?.obj?.component_name ?? "Unknown",
          change: "modified",
          details,
        });
        compsModified++;
      }
    }
  }
  for (const [id, oldComp] of Object.entries(oldComps)) {
    if (!(id in newComps)) {
      diffComps.push({
        id,
        componentName: oldComp?.obj?.component_name ?? "Unknown",
        change: "removed",
      });
      compsRemoved++;
    }
  }

  const isEmpty =
    diffNodes.length === 0 && diffEdges.length === 0 && diffComps.length === 0;

  return {
    nodes: diffNodes,
    edges: diffEdges,
    components: diffComps,
    stats: {
      nodesAdded, nodesRemoved, nodesModified,
      edgesAdded, edgesRemoved, edgesModified,
      componentsAdded: compsAdded, componentsRemoved: compsRemoved, componentsModified: compsModified,
    },
    isEmpty,
  };
}
