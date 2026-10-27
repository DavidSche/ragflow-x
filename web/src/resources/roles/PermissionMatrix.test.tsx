import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PermissionMatrix } from "./PermissionMatrix";

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

describe("PermissionMatrix", () => {
  it("renders and edits fine-grained governance permissions", () => {
    const onChange = vi.fn();
    render(
      <PermissionMatrix
        catalog={[
          { resource: "tenant", actions: ["read", "governance.read_sensitive", "tenant.connection.rotate_secret"] },
          { resource: "enterprise-connection", actions: ["read", "manage", "test"] },
          { resource: "document", actions: ["read", "append", "delete:own", "execute"] },
          { resource: "chat", actions: ["execute"] },
          { resource: "search-app", actions: ["execute"] },
          { resource: "agent", actions: ["execute"] },
          { resource: "scenario-template", actions: ["read"] },
          { resource: "prompt-policy", actions: ["read"] },
          { resource: "knowledge-lifecycle", actions: ["read"] },
          { resource: "eval-set", actions: ["read"] },
        ]}
        mapState={{}}
        builtin={false}
        disabled={false}
        onChange={onChange}
      />,
    );

    expect(screen.getByRole("columnheader", { name: "governance.read_sensitive" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "append" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "delete:own" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "test" })).toBeInTheDocument();
    expect(screen.getByLabelText("roles.res_workbench execute")).toBeInTheDocument();
    expect(screen.getByLabelText("roles.res_conversation_center execute")).toBeInTheDocument();
    expect(screen.getByLabelText("roles.res_asset_governance execute")).toBeInTheDocument();
    const selector = screen.getByLabelText("tenant tenant.connection.rotate_secret");
    fireEvent.change(selector, { target: { value: "allow" } });
    expect(onChange).toHaveBeenCalledWith("tenant", "tenant.connection.rotate_secret", "allow");
  });
});
