# gh-agent

本机轮询 GitHub，把 issue / PR 评论交给**本地模型**处理并回帖的小工具。

不依赖 GitHub Actions、不开公网端口、不装 GitHub App：只做**出站**请求
（读 GitHub REST + 调本地 OpenAI 兼容端点），跑在你自己机器上。

两种形态共用同一套内核：

| 形态 | 入口 | 回帖方式 |
|---|---|---|
| 桌面应用（托盘常驻） | `go run .` / `npm run dev` | 出稿进「待审」，你点批准才发 |
| CLI | `go run ./cmd/gh-agent` | 默认干跑打印；`--live` 直接发 |

## 桌面应用

```bash
npm install          # 前端依赖（Vite / React / mygo-cli）
npm run dev          # 起 Vite + 编译并启动应用，改 .go 或 .tsx 都会自动重载
npm run build        # 打包到 build/
```

窗口三块：**待审**（草稿 + markdown 预览 + 批准/驳回）、**事件流**（轮询日志）、
**设置**（仓库、间隔、触发词、模型端点）。关掉窗口不退出，点托盘图标再开。

草稿落盘在 `%AppData%/gh-agent/drafts.json`，重启不丢。

> 桌面壳永远走人工审核，不会自己发帖。要全自动，用 CLI 的 `--live`。

## CLI

```bash
# 1. 生成默认配置（写在 %AppData%/gh-agent/config.json）
go run ./cmd/gh-agent --print-config > /dev/null

# 2. 改配置：仓库、模型端点、触发词
#    默认指向本机 magpie: http://127.0.0.1:3425/v1

# 3. 干跑一轮（只打印，不发评论）
go run ./cmd/gh-agent --once

# 4. 真发
go run ./cmd/gh-agent --once --live

# 5. 常驻
go run ./cmd/gh-agent
```

Token 解析顺序：`GH_TOKEN` → `GITHUB_TOKEN` → `gh auth token`（复用已登录的 gh，不落盘）。

## 触发规则

一条评论同时满足才会被处理：

1. 作者不是自己（按 login）、不是 bot；
2. `author_association` 在允许列表（默认 OWNER / MEMBER / COLLABORATOR / CONTRIBUTOR）；
3. 正文命中 `trigger`（默认 `@claude`，大小写不敏感）；
4. 这条 comment id 没处理过（state 里记最近 1000 条）。

## 目录

```
main.go              桌面应用入口（托盘 + 窗口 + Bind）
index.html, src/     前端（React + Vite），src/mygo.ts 是 mygo generate 生成的类型化客户端
mygo.config.ts       应用名、标识、打包方式
cmd/gh-agent/        CLI 入口与参数
internal/bootstrap/  装配：token → GitHub 客户端 → 模型客户端 → Runner（CLI 和桌面共用）
internal/desk/       桌面服务层：配置读写、轮询控制、待审队列、事件推送
internal/config/     配置读写与校验
internal/ghapi/      GitHub REST 最小封装（issue / comment / 回帖）
internal/llm/        OpenAI 兼容客户端（magpie / ollama / vllm）
internal/agent/      过滤规则、prompt 组装、轮询器
internal/state/      游标与已处理 id（按仓库分开）
```

## 工具链

MyGo 要求 **Go 1.27.3+**。本仓库 `go.mod` 已声明 `go 1.27.3`，
Go 的 `GOTOOLCHAIN=auto` 会自动下载对应工具链，**不需要动系统里装的 Go**。

## 已知不做

- 不自动合并 PR、不改代码、不开 PR —— 只回评论。
- 不处理 GitHub Discussions（需要 GraphQL，等有需求再说）。
- PR 行内评论（review comments）暂不支持。
