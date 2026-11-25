import { beforeEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { BrandLogo, BrandName } from "./BrandIdentity";
import { DEFAULT_BRAND_NAME, resetBranding, setBranding } from "../lib/branding";

describe("BrandIdentity", () => {
  beforeEach(() => {
    resetBranding();
  });

  it("renders the configured logo and name", () => {
    setBranding({ name: "山东数据集团", logo: "/logo.png" });
    render(<BrandLogo />);
    render(<BrandName />);
    expect(screen.getByTestId("brand-logo")).toHaveAttribute("src", "/logo.png");
    expect(screen.getByTestId("brand-logo")).toHaveAttribute("alt", "山东数据集团");
    expect(screen.getByTestId("brand-name")).toHaveTextContent("山东数据集团");
  });

  it("renders the default glyph and name when nothing is configured", () => {
    render(<BrandLogo />);
    render(<BrandName />);
    expect(screen.queryByTestId("brand-logo")).toBeNull();
    expect(screen.getByTestId("brand-logo-default")).toBeInTheDocument();
    expect(screen.getByTestId("brand-name")).toHaveTextContent(DEFAULT_BRAND_NAME);
  });

  it("scales a configured logo by height instead of forcing it into a square", () => {
    // A wide wordmark squeezed into a 20px square was the reported "logo 不清晰";
    // the lockup must let width follow the file's aspect ratio.
    setBranding({ name: "山东数据集团", logo: "/wordmark.png" });
    render(<BrandLogo />);
    const logo = screen.getByTestId("brand-logo");
    expect(logo).toHaveClass("h-7", "w-auto", "object-contain");
    expect(logo.className).not.toMatch(/\bsize-\d/);
  });

  it("keeps a configured logo when only the name is empty", () => {
    setBranding({ name: "", logo: "/only-logo.png" });
    render(<BrandLogo />);
    render(<BrandName />);
    expect(screen.getByTestId("brand-logo")).toHaveAttribute("src", "/only-logo.png");
    expect(screen.getByTestId("brand-name")).toHaveTextContent(DEFAULT_BRAND_NAME);
  });

  it("lets the system name shrink so it truncates instead of overflowing", () => {
    setBranding({ name: "山东数据集团有限公司知识运营平台", logo: "" });
    render(<BrandName />);
    expect(screen.getByTestId("brand-name")).toHaveClass("min-w-0", "truncate");
  });
});
