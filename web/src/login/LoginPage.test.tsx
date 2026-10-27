import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock(import("ra-core"), async (importOriginal) => {
  const actual = await importOriginal<typeof import("ra-core")>();
  return {
    ...actual,
    Form: ({ children }: { children: ReactNode }) => <form>{children}</form>,
    useLogin: () => (params: unknown, pathName?: string) =>
      Promise.resolve({ params, pathName }),
  };
});

vi.mock("@/components/admin/text-input", () => ({
  TextInput: () => <input aria-label="用户名" />,
}));

vi.mock("../lib/api", () => ({
  api: { get: getMock },
  unwrap: async (request: Promise<{ data: { data: unknown } }>) => {
    const response = await request;
    return response.data.data;
  },
  ApiError: class ApiError extends Error {},
}));

import { LoginPage } from "./LoginPage";

describe("LoginPage OIDC", () => {
  beforeEach(() => {
    getMock.mockReset();
    getMock.mockImplementation((url: string) => {
      if (url === "/branding/public") return Promise.resolve({ data: { code: 0, data: { name: "RAGFlow-X", logo: "" } } });
      if (url === "/system/setup/status") return Promise.resolve({ data: { code: 0, data: { initialized: true } } });
      if (url === "/auth/oidc/status") return Promise.resolve({ data: { code: 0, data: { enabled: true } } });
      return Promise.reject(new Error("unexpected request"));
    });
  });

  it("shows enterprise SSO only when OIDC is enabled", async () => {
    render(<LoginPage />);
    expect(await screen.findByRole("link", { name: /企业账号登录/ })).toHaveAttribute(
      "href",
      "/api/v1/auth/oidc/start",
    );
  });
});
