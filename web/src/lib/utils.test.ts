import { describe, expect, test } from "vitest";
import { cn } from "./utils";

describe("cn", () => {
  test("joins truthy class names", () => {
    expect(cn("a", "b", false, null, undefined, 0)).toBe("a b");
  });

  test("lets tailwind-merge resolve conflicting utilities", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
    expect(cn("bg-red-500", "bg-blue-500", "text-white")).toBe("bg-blue-500 text-white");
  });

  test("returns empty string for no inputs", () => {
    expect(cn()).toBe("");
  });
});
