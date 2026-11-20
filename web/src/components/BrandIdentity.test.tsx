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

  it("keeps a configured logo when only the name is empty", () => {
    setBranding({ name: "", logo: "/only-logo.png" });
    render(<BrandLogo />);
    render(<BrandName />);
    expect(screen.getByTestId("brand-logo")).toHaveAttribute("src", "/only-logo.png");
    expect(screen.getByTestId("brand-name")).toHaveTextContent(DEFAULT_BRAND_NAME);
  });
});
