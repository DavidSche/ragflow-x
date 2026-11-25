import { describe, expect, it } from "vitest";
import indexHtml from "../../index.html?raw";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * T28 / E156 — the shell is where a phone's two cheapest defects live.
 *
 * Both are one-string facts about files Vite copies verbatim, so they are
 * checked as strings rather than rendered: a missing `interactive-widget` does
 * not fail any component test, it just means the software keyboard covers the
 * input the user is typing into.
 */
// vitest runs with `web/` as its root, so this is web/src/index.css. A
// missing file throws rather than skipping the assertion.
const indexCss = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

describe("mobile shell", () => {
  it("lets the software keyboard resize the layout instead of covering it", () => {
    const viewport = indexHtml.match(/<meta name="viewport" content="([^"]+)"/)?.[1] ?? "";
    expect(viewport).toContain("interactive-widget=resizes-content");
    expect(viewport).toContain("width=device-width");
  });

  it("claims the notch instead of letting content sit under it", () => {
    const viewport = indexHtml.match(/<meta name="viewport" content="([^"]+)"/)?.[1] ?? "";
    expect(viewport).toContain("viewport-fit=cover");
    // viewport-fit=cover without safe-area padding moves the defect from
    // "content clipped" to "content under the home indicator".
    expect(indexCss).toContain("env(safe-area-inset-bottom)");
    expect(indexCss).toContain("env(safe-area-inset-left)");
  });

  it("keeps the identity viewport for browsers that ignore the extra keys", () => {
    const viewport = indexHtml.match(/<meta name="viewport" content="([^"]+)"/)?.[1] ?? "";
    expect(viewport).toContain("initial-scale=1.0");
    expect(viewport).not.toContain("user-scalable=no");
  });
});
