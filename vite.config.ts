import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    // `mygo dev` 让应用加载这个地址（mygo.config.ts 的 devUrl）。
    port: 5173,
    strictPort: true,
    // 开发产物和打包产物不是页面，别让 Vite 去监听它们。
    watch: { ignored: ["**/.mygo/**", "**/build/**"] },
  },
});
