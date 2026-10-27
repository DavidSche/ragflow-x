import { describe, expect, it, vi } from "vitest";
import { api } from "../../lib/api";
import { validateEmailFormat, validateUniqueUsername } from "./user-validators";

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn() },
}));

describe("user validators", () => {
  it("rejects invalid email addresses and accepts blank values", () => {
    expect(validateEmailFormat("not-an-email")).toBe("email");
    expect(validateEmailFormat("user@example.com")).toBeUndefined();
    expect(validateEmailFormat("")).toBeUndefined();
  });

  it("checks username availability case-insensitively", async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { code: 0, data: { items: [{ username: "Alice" }] } },
    } as never);
    await expect(validateUniqueUsername("alice", "taken")).resolves.toBe("taken");

    vi.mocked(api.get).mockResolvedValue({
      data: { code: 0, data: { items: [] } },
    } as never);
    await expect(validateUniqueUsername("alice", "taken")).resolves.toBeUndefined();
  });
});
