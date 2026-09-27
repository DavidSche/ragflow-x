import { defineConfig, devices } from "@playwright/test";

// Frontend E2E smoke (A4, doc/33 Sprint P1-2).
//
// The spec runs against the production bundle served by `vite preview` and
// mocks all `/api/v1/**` traffic in-page, so the suite is self-contained and
// CI-safe: no live ragflow-x backend or RAGFlow instance is required. It
// complements the backend e2e_test.go suite at the HTTP/SSE layer.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: [
    [process.env.CI ? "github" : "list"],
    ["json", { outputFile: "../test-results/playwright.json" }],
  ],
  use: {
    baseURL: "http://localhost:4173",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "mobile-chrome",
      use: { ...devices["Pixel 7"] },
    },
  ],
  webServer: {
    command: "npm run build && npm run preview -- --port 4173 --strictPort",
    url: "http://localhost:4173",
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
