export function clampNumber(
  value: number,
  min: number,
  max: number,
  fallback = min,
): number {
  const bounded = Math.min(Math.max(value, min), max);
  return Number.isFinite(bounded) ? bounded : fallback;
}

export function clampInteger(
  value: number,
  min: number,
  max: number,
  fallback = min,
): number {
  return Math.round(clampNumber(value, min, max, fallback));
}

export function parseBoundedInteger(
  value: string,
  min: number,
  max: number,
  fallback = min,
): number {
  return clampInteger(Number(value), min, max, fallback);
}
