import type { ResourceProps } from "ra-core";
import { Palette } from "lucide-react";
import { BrandingPage } from "./BrandingPage";

export const branding: ResourceProps = {
  name: "branding",
  list: BrandingPage,
  icon: Palette,
};
