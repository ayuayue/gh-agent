# gh-agent

本机轮询 GitHub，把 issue / PR 评论交给**本地模型**处理并回帖的小工具。

不依赖 GitHub Actions、不开公网端口、不装 GitHub App：只做**出站**请求
（读 GitHub REST + 调本地 OpenAI 兼容端点），跑在你自己机器上。

## 现在到哪一步

- **D1（当前）**：CLI 内核 —— 配置、轮询、去重、生成回复、干跑输出。
- D2：MyGo 壳（托盘 + 窗口 + 配置页），见文末。

## 快速开始

```bash
# 1. 生成默认配置（写在 %AppData%/gh-agent/config.json）
go run ./cmd/gh-agent --print-config > /dev/null

# 2. 改配置：仓库、模型端点、触发词
#    默认指向本机 magpie: http://127.0.0.1:3425/v1

# 3. 干跑一轮（只打印，不发评论）
go run ./cmd/gh-agent --once

# 4. 真发
go run ./cmd/gh-agent --once --live
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
cmd/gh-agent/        入口与参数
internal/config/     配置读写与校验
internal/ghapi/      GitHub REST 最小封装（issue / comment / 回帖）
internal/llm/        OpenAI 兼容客户端（magpie / ollama / vllm）
internal/agent/      过滤规则、prompt 组装、轮询器
internal/state/      游标与已处理 id（按仓库分开）
```

## 后续：D2 用 MyGo 加壳

MyGo（`egoist/mygo`）要求 **Go 1.27.3+**；本仓库当前用 1.26 是为了先跑通内核，
加壳时把 `go.mod` 的 `go` 指令升到 1.27.3 即可（`mise` 里已能装到）。

界面形态定为：**web 前端窗口（Vite + TS）画配置和事件流，托盘常驻**，
原生 `ui` 包不用来画日志和 markdown。

## 已知不做

- 不自动合并 PR、不改代码、不开 PR —— 只回评论。
- 不处理 GitHub Discussions（需要 GraphQL，等有需求再说）。
