import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    // The output directory holds one tracked file, placeholder.html, which the
    // Go binary embeds and serves when this bundle has not been built. Emptying
    // the directory would delete it on every build.
    outDir: "../plugins/webui/dist",
    emptyOutDir: false,
  },
});
