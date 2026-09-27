import path from "path";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// `@` maps to this project's own src. Components from shadcn-admin-kit are
// copied into src/components, src/hooks and src/lib following the kit's
// registry-based install, so the build no longer depends on the sibling
// shadcn-admin-kit directory.
const srcDir = path.resolve(import.meta.dirname, "src");

const excludeMermaidInitialPreload = {
  name: "exclude-mermaid-initial-preload",
  transformIndexHtml(html: string) {
    return html.replace(
      /[ \t]*<link rel="modulepreload"[^>]*href="\/assets\/vendor-mermaid[^"]*"[^>]*>\r?\n?/g,
      ""
    );
  },
};

export default defineConfig({
  plugins: [react(), tailwindcss(), excludeMermaidInitialPreload],
  build: {
    rollupOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: "vendor-mermaid", test: /node_modules[\\/](@mermaid-js[\\/]|mermaid[\\/]|cytoscape|katex)/, priority: 50 },
            { name: "vendor-react", test: /node_modules[\\/](react|react-dom|react-router)[\\/]/, priority: 40 },
            { name: "vendor-admin", test: /node_modules[\\/](react-admin|ra-core|ra-data)[\\/]/, priority: 30 },
            { name: "vendor-radix", test: /node_modules[\\/]@radix-ui[\\/]/, priority: 25 },
            { name: "vendor-query", test: /node_modules[\\/]@tanstack[\\/]/, priority: 25 },
            { name: "vendor-forms", test: /node_modules[\\/](react-hook-form|@hookform[\\/]|zod|date-fns)[\\/]/, priority: 25 },
            { name: "vendor-i18n", test: /node_modules[\\/](i18next|react-i18next|intl-messageformat)[\\/]/, priority: 25 },
            { name: "vendor-icons", test: /node_modules[\\/]lucide-react[\\/]/, priority: 25 },
            { name: "vendor-charts", test: /node_modules[\\/](recharts|d3-)[\\/]/, priority: 20 },
            { name: "vendor-flow", test: /node_modules[\\/]@xyflow[\\/]/, priority: 20 },
            { name: "vendor-markdown", test: /node_modules[\\/](react-markdown|remark-gfm|react-medium-image-zoom)[\\/]/, priority: 20 },
            { name: "vendor", test: /node_modules[\\/]/, priority: 0 },
          ],
        },
      },
    },
  },
  resolve: {
    alias: {
      "@": srcDir,
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:9191",
        changeOrigin: true,
      },
    },
  },
});
