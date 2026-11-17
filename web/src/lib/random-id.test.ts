// ScenarioID: SC-FE-DATA-001
import { afterEach, describe, expect, test, vi } from "vitest";
import { createRandomId } from "./random-id";

const originalCrypto = globalThis.crypto;

function stubCrypto(value: unknown) {
  Object.defineProperty(globalThis, "crypto", { value, configurable: true, writable: true });
}

afterEach(() => {
  stubCrypto(originalCrypto);
  vi.restoreAllMocks();
});

describe("createRandomId", () => {
  test("uses crypto.randomUUID when the context is secure", () => {
    stubCrypto({ randomUUID: () => "11111111-2222-3333-4444-555555555555" });
    expect(createRandomId()).toBe("11111111-2222-3333-4444-555555555555");
  });

  // The 4.151 deployment is plain http on a LAN ip, i.e. not a secure context:
  // crypto exists but randomUUID does not. That must not throw, or every page
  // that mints a request id dies in the error boundary.
  test("falls back when crypto.randomUUID is unavailable (insecure context)", () => {
    stubCrypto({});
    const id = createRandomId();
    expect(id.length).toBeGreaterThan(8);
    expect(id).toMatch(/^[0-9a-z-]+$/);
  });

  test("falls back when crypto itself is missing", () => {
    stubCrypto(undefined);
    expect(() => createRandomId()).not.toThrow();
    expect(createRandomId()).not.toBe(createRandomId());
  });

  test("fallback ids do not collide inside one millisecond", () => {
    stubCrypto({});
    const now = 1_700_000_000_000;
    vi.spyOn(Date, "now").mockReturnValue(now);
    const ids = new Set(Array.from({ length: 200 }, () => createRandomId()));
    expect(ids.size).toBe(200);
  });
});
