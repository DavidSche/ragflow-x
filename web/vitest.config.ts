import path from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Frontend unit/component tests (A4, doc/33 Sprint P1-2).
// `@` resolves to src, matching vite.config.ts. jsdom + Testing Library cover
// dataProvider / utilities / workbench components; Playwright E2E lives in
// e2e/ and is intentionally excluded here.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "src") },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    reporters: [
      "default",
      ["json", { outputFile: "../test-results/vitest.json" }],
    ],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    exclude: [
      "node_modules/**",
      "dist/**",
      "dist2/**",
      "dist3/**",
      "e2e/**",
      "playwright.config.ts",
      // Legacy react-admin kit specs (src/components/admin/*.spec.tsx) depend on
      // vitest-browser-react and @/stories, which are not installed in this app;
      // they are out of A4 scope and must not block `npm run test` in CI.
      "src/components/admin/**",
    ],
    coverage: {
      provider: "v8",
      reporter: ["text", "html", "json", "json-summary"],
      include: [
        "src/dataProvider.ts",
        "src/lib/utils.ts",
        "src/lib/errors.ts",
        "src/hooks/use-mobile.ts",
        "src/resources/workbench/workbench-types.ts",
        "src/resources/workbench/InlineCitations.tsx",
        "src/resources/workbench/CitationLink.tsx",
      ],
      exclude: ["src/**/*.{test,spec}.{ts,tsx}", "src/test/**", "src/vite-env.d.ts"],
      thresholds: {
        statements: 65,
        functions: 60,
        lines: 65,
        branches: 50,
      },
    },
  },
});
