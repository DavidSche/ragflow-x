import { useSyncExternalStore } from "react";
import { api } from "./api";
import { optionalWarning } from "./optional-error";

export interface Branding {
  name: string;
  logo: string;
}

let brand: Branding = { name: "RAGFlow-X", logo: "" };
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

export function setBranding(next: Branding) {
  brand = { name: next.name || "RAGFlow-X", logo: next.logo || "" };
  emit();
}

export function loadBranding() {
  api
    .get<{ code: number; data?: Branding }>("/branding")
    .then((r) => {
      if (r.data.code === 0 && r.data.data) setBranding(r.data.data);
    })
    .catch((error) => optionalWarning(error, "品牌信息加载失败"));
}

export function useBranding(): Branding {
  return useSyncExternalStore(subscribeBranding, getBranding);
}
