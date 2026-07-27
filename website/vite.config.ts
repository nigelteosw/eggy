import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../plugins/webui/dist",
    emptyOutDir: true,
  },
});
