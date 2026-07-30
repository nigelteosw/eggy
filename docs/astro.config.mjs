import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://nigelteosw.github.io",
  base: "/eggy/docs",
  output: "static",
  trailingSlash: "always",
  build: {
    format: "directory",
  },
});
