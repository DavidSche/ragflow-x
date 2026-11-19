/**
 * Engine timestamps and the two units they arrive in.
 *
 * RAGFlow writes epoch **milliseconds** for agent versions and sessions
 * (`update_time: 1795048252290`) but epoch **seconds** for a few older fields.
 * The version tab multiplied by 1000 unconditionally, so a millisecond value
 * rendered as "58852/10/24 18:31:30" — a year no release will ever reach.
 *
 * This mirrors the backend's unit-sniffing helper (`ragflowUpdateTime` in
 * internal/service/resource_sync.go) so one value cannot mean two instants
 * depending on which layer printed it.
 */

const SECOND_MAX = 100_000_000_000; // ~year 5138 in seconds
const MILLI_MAX = 100_000_000_000_000; // ~year 5138 in milliseconds
const MICRO_MAX = 100_000_000_000_000_000;

/** Epoch seconds for a value of unknown unit; null when it is not a timestamp. */
export function epochSeconds(value?: number | null): number | null {
  const raw = Number(value);
  if (!Number.isFinite(raw) || raw <= 0) return null;
  if (raw < SECOND_MAX) return raw;
  if (raw < MILLI_MAX) return raw / 1000;
  if (raw < MICRO_MAX) return raw / 1_000_000;
  return raw / 1_000_000_000;
}

/** Localised date-time for an engine timestamp; null when absent/invalid. */
export function formatEpoch(value?: number | null): string | null {
  const seconds = epochSeconds(value);
  if (seconds === null) return null;
  const date = new Date(seconds * 1000);
  if (Number.isNaN(date.getTime())) return null;
  return date.toLocaleString();
}
