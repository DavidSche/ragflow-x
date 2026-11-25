import { Shell } from "lucide-react";
import { DEFAULT_BRAND_NAME, useBranding } from "../lib/branding";

interface BrandLogoProps {
  /** Tailwind size classes; height drives the scale, width follows the image's aspect ratio. */
  className?: string;
}

/**
 * Lockup height for the authenticated shell (sidebar header): 28px.
 *
 * The configured file can be a square mark or a wide wordmark, so the logo is
 * scaled by height and keeps its natural aspect ratio (`w-auto`) inside a
 * `max-w-28` (112px) box. The previous `!size-5` forced every logo into a 20px
 * square: a wordmark was squeezed into 20px of width and read as "logo 不清晰".
 * On 2x displays 28px needs a source image at least 56px tall — 64px+ is the
 * documented recommendation in LogoPickerRow.
 */
export const SIDEBAR_LOGO_CLASS =
  "h-7 w-auto max-w-28 shrink-0 object-contain group-data-[collapsible=icon]:h-5 group-data-[collapsible=icon]:max-w-5";

/**
 * Logo glyph for the authenticated shell: the configured brand logo when the
 * platform/tenant set one, otherwise the built-in shell mark.
 */
export function BrandLogo({ className = SIDEBAR_LOGO_CLASS }: BrandLogoProps) {
  const brand = useBranding();
  const name = brand.name || DEFAULT_BRAND_NAME;
  if (brand.logo) {
    return (
      <img
        src={brand.logo}
        alt={name}
        data-testid="brand-logo"
        decoding="async"
        className={className}
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
    <span className={`min-w-0 ${className}`} data-testid="brand-name">
      {brand.name || DEFAULT_BRAND_NAME}
    </span>
  );
}
