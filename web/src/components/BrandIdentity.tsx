import { Shell } from "lucide-react";
import { DEFAULT_BRAND_NAME, useBranding } from "../lib/branding";

interface BrandLogoProps {
  /** Tailwind size classes, e.g. `!size-5` (sidebar) or `size-8` (login card). */
  className?: string;
}

/**
 * Logo glyph for the authenticated shell: the configured brand logo when the
 * platform/tenant set one, otherwise the built-in shell mark.
 */
export function BrandLogo({ className = "!size-5" }: BrandLogoProps) {
  const brand = useBranding();
  const name = brand.name || DEFAULT_BRAND_NAME;
  if (brand.logo) {
    return (
      <img
        src={brand.logo}
        alt={name}
        data-testid="brand-logo"
        className={`${className} object-contain`}
      />
    );
  }
  return <Shell className={className} data-testid="brand-logo-default" aria-hidden="true" />;
}

/**
 * System name for the authenticated shell: the configured brand name when set,
 * otherwise the default `RAGFlow-X`. Mirrors the login page, which reads the
 * same platform branding from `/branding/public`.
 */
export function BrandName({ className = "text-base font-semibold truncate" }: BrandLogoProps) {
  const brand = useBranding();
  return (
    <span className={className} data-testid="brand-name">
      {brand.name || DEFAULT_BRAND_NAME}
    </span>
  );
}
