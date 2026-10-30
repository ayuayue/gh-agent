import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "GH Agent",
  identifier: "com.ayuayue.ghagent",
  version: "0.2.0",
  // `mygo dev` 跑 devCommand 并加载 devUrl；`mygo build` 跑 buildCommand 并把
  // frontendDist 打进应用里。这台机器没有 bun，所以脚本走 npm。
  devUrl: "http://localhost:5173",
  devCommand: "npm run dev:web",
  buildCommand: "npm run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
