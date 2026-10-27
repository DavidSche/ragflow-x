import path from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

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
      ["json", { outputFile: "../test-results/vitest-coverage-report.json" }],
    ],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    exclude: ["node_modules/**", "dist/**", "e2e/**", "src/components/admin/**"],
    coverage: {
      provider: "v8",
      reportsDirectory: "coverage-report",
      reporter: [["json", { file: "coverage-report-final.json" }]],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/**/*.{test,spec}.{ts,tsx}", "src/test/**", "src/vite-env.d.ts"],
    },
  },
});
