import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "/app/encbackup/",
  plugins: [vue()],
  server: {
    port: 5176,
    proxy: {
      "/app/encbackup": "http://localhost:5175"
    }
  }
});
