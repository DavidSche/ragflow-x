import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  DEFAULT_BRAND_NAME,
  getBranding,
  loadBranding,
  resetBranding,
} from "./branding";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("./api", () => ({ api: { get: getMock } }));
vi.mock("./optional-error", () => ({ optionalWarning: () => {} }));

const ok = (name: string, logo: string) =>
  Promise.resolve({ data: { code: 0, data: { name, logo } } });

describe("branding store", () => {
  beforeEach(() => {
    getMock.mockReset();
    resetBranding();
  });

  it("defaults to the built-in name until branding loads", () => {
    expect(getBranding()).toEqual({ name: DEFAULT_BRAND_NAME, logo: "" });
  });

  it("applies the caller tenant branding from /branding", async () => {
    getMock.mockImplementation(() => ok("山东数据集团", "/api/v1/static/logos/a.png"));
    await loadBranding();
    expect(getBranding()).toEqual({ name: "山东数据集团", logo: "/api/v1/static/logos/a.png" });
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getMock.mock.calls[0][0]).toBe("/branding");
  });

  it("falls back to /branding/public when the caller lacks branding:read", async () => {
    getMock.mockImplementation((url: string) =>
      url === "/branding" ? Promise.reject(new Error("403")) : ok("平台品牌", "/logo.png"),
    );
    await loadBranding();
    expect(getBranding()).toEqual({ name: "平台品牌", logo: "/logo.png" });
    expect(getMock.mock.calls.map((c) => c[0])).toEqual(["/branding", "/branding/public"]);
  });

  it("falls back to /branding/public when the tenant endpoint answers non-zero", async () => {
    getMock.mockImplementation((url: string) =>
      url === "/branding"
        ? Promise.resolve({ data: { code: 40003, data: undefined } })
        : ok("平台品牌", ""),
    );
    await loadBranding();
    expect(getBranding().name).toBe("平台品牌");
  });

  it("keeps the default when both endpoints fail", async () => {
    getMock.mockImplementation(() => Promise.reject(new Error("boom")));
    await loadBranding();
    expect(getBranding()).toEqual({ name: DEFAULT_BRAND_NAME, logo: "" });
  });

  it("treats an empty configured name as unset", async () => {
    getMock.mockImplementation(() => ok("", ""));
    await loadBranding();
    expect(getBranding()).toEqual({ name: DEFAULT_BRAND_NAME, logo: "" });
  });

  it("shares one request between concurrent callers", async () => {
    getMock.mockImplementation(() => ok("并发品牌", ""));
    await Promise.all([loadBranding(), loadBranding()]);
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getBranding().name).toBe("并发品牌");
  });
});
