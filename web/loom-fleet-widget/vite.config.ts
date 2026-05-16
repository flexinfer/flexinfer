import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { viteSingleFile } from "vite-plugin-singlefile";

// MCP Apps hosts (Claude Code Desktop, ChatGPT Apps SDK) render
// widgets in sandboxed iframes from a single HTML resource. The
// runtime cannot fetch additional JS/CSS chunks, so everything must
// be inlined into one file at build time.
//
// The Go MCP server (cmd/mcp-loom-widget) embeds the built file via
// //go:embed and serves it as the ui:// resource body. The Makefile
// `widget` target builds here, then copies dist/index.html into
// cmd/mcp-loom-widget/widget.html.
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  build: {
    target: "es2020",
    cssCodeSplit: false,
    assetsInlineLimit: 100_000_000,
    chunkSizeWarningLimit: 2000,
    rollupOptions: {
      // viteSingleFile flattens the graph; keep entry small and avoid
      // dynamic imports so nothing splits out into a separate chunk.
      output: { inlineDynamicImports: true },
    },
  },
});
