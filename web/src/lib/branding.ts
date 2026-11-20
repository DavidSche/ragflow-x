import { useSyncExternalStore } from "react";
import { api } from "./api";
import { optionalWarning } from "./optional-error";

export interface Branding {
  name: string;
  logo: string;
}

/** Default system name, used when neither the tenant nor the platform configured one. */
export const DEFAULT_BRAND_NAME = "RAGFlow-X";

interface Envelope<T> {
  code: number;
  data?: T;
}

let brand: Branding = { name: DEFAULT_BRAND_NAME, logo: "" };
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((l) => l());
}

export function subscribeBranding(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getBranding(): Branding {
  return brand;
}

/** Restores the built-in default branding (used by tests and after logout). */
export function resetBranding() {
  brand = { name: DEFAULT_BRAND_NAME, logo: "" };
  emit();
}

export function setBranding(next: Branding) {
  brand = { name: next.name || DEFAULT_BRAND_NAME, logo: next.logo || "" };
  emit();
}

let inflight: Promise<void> | null = null;

function apply(r: { data: Envelope<Branding> }): boolean {
  if (r.data.code === 0 && r.data.data) {
    setBranding(r.data.data);
    return true;
  }
  return false;
}

/**
 * Loads the effective branding: the caller tenant's own name/logo, else the
 * platform branding (server-side fallback), else the default.
 *
 * `/branding` needs the `branding:read` permission, which ordinary users may
 * not have; in that case we fall back to the public endpoint so every role sees
 * the same configured name and logo instead of the hard-coded default.
 * Concurrent callers (sidebar + brand config page) share one request.
 */
export function loadBranding(): Promise<void> {
  if (inflight) return inflight;
  inflight = api
    .get<Envelope<Branding>>("/branding")
    .then((r) => {
      if (apply(r)) return;
      return api.get<Envelope<Branding>>("/branding/public").then((p) => {
        apply(p);
      });
    })
    .catch((error) => {
      optionalWarning(error, "品牌信息加载失败");
      return api
        .get<Envelope<Branding>>("/branding/public")
        .then((p) => {
          apply(p);
        })
        .catch((publicError) => {
          optionalWarning(publicError, "登录页品牌信息加载失败");
        });
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

export function useBranding(): Branding {
  return useSyncExternalStore(subscribeBranding, getBranding);
}
