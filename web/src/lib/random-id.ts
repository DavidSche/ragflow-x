/**
 * Client ids for `request_id`, `Idempotency-Key` and composer drafts.
 *
 * `crypto.randomUUID()` only exists in a secure context (https, localhost). The
 * 4.151 drill serves the console over `http://192.168.4.151:19193`, where it is
 * `undefined`: the conversation centre called it during render, threw
 * `CSPRNG unavailable`, and the whole page dropped into the error boundary
 * (2026-11-17 console: `Error: CSPRNG unavailable at TT/DT … at useState`).
 *
 * These ids only have to be *unique*, not unguessable — the server scopes the
 * idempotency key to the caller and treats a replay as the same request — so a
 * time+random fallback is the correct behaviour here, not a downgrade of a
 * security property. `approval-hold.ts` already carried this fallback; this is
 * the single implementation every caller now shares.
 */
export function createRandomId(): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  if (uuid) return uuid;
  return [
    Date.now().toString(36),
    Math.random().toString(36).slice(2),
    Math.random().toString(36).slice(2),
  ].join("-");
}
