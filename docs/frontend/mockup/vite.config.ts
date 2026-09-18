import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    fs: {
      // tokens.css lives one directory above the mockup and is imported by
      // app.css so the mockup consumes the exact shipping token file.
      allow: [".."],
    },
  },
});
