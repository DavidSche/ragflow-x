import { afterEach, describe, expect, it, vi } from "vitest";
import { pemToArrayBuffer, sealRSAOAEPSHA256 } from "./seal";

describe("sealRSAOAEPSHA256", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("parses PEM key material to DER bytes", () => {
    const bytes = new Uint8Array(pemToArrayBuffer("-----BEGIN PUBLIC KEY-----\nAQID\n-----END PUBLIC KEY-----"));
    expect(Array.from(bytes)).toEqual([1, 2, 3]);
  });

  it("seals plaintext with RSA-OAEP/SHA-256 and returns base64", async () => {
    const importKey = vi.fn().mockResolvedValue("crypto-key");
    const encrypt = vi.fn().mockResolvedValue(Uint8Array.from([104, 105]).buffer);
    vi.stubGlobal("crypto", { subtle: { importKey, encrypt } });

    const sealed = await sealRSAOAEPSHA256("-----BEGIN PUBLIC KEY-----\nAQID\n-----END PUBLIC KEY-----", "hi");

    expect(sealed).toBe("aGk=");
    expect(importKey).toHaveBeenCalledWith(
      "spki",
      expect.any(ArrayBuffer),
      { name: "RSA-OAEP", hash: "SHA-256" },
      false,
      ["encrypt"],
    );
    expect(encrypt).toHaveBeenCalledTimes(1);
    expect(encrypt.mock.calls[0][2]).toEqual(new TextEncoder().encode("hi"));
  });
});
