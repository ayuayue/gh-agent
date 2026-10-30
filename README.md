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

## 出稿方：模型端点 还是 本地 agent

配置里的 `agent` 决定谁来写这条回复：

| 值 | 怎么跑 | 能看到代码 | 单条耗时 |
|---|---|---|---|
| `llm`（默认） | 打 `provider.base_url` 的 OpenAI 兼容端点 | 否，只看 prompt 里的上下文 | 数秒 |
| `codebuddy` | `codebuddy --acp --acp-transport stdio` | 是 | 十几秒到一分钟 |
| `pi` | `npx -y pi-acp` | 是 | 类似 |

本地 agent 走 **ACP（Agent Client Protocol）**，跟编辑器接 agent 是同一套协议。
ACP 官方只提供 Kotlin / Python / Rust / TypeScript 的 SDK，没有 Go 版，
所以 `internal/acp` 是按官方 v1 schema 自己实现的客户端（JSON-RPC over stdio，
只用到 `initialize` / `session/new` / `session/prompt` / `session/update` 四个方法）。

agent 要读代码，得先告诉它仓库在本地哪：

```json
"agent": "codebuddy",
"repo_paths": { "ayuayue/PiDeck": "D:/project/pideck" }
```

留空的话会在配置目录下开一个空的 `work/<repo>`，agent 只能靠 prompt 里的上下文回答。

界面上每条草稿都能「换 agent 重做」：同一个 prompt 换一个 agent 再出一遍，方便对比；
也可以「编辑」就地改正文再发。

### agent 改代码

工作目录是 git 仓库时，agent 会被告知「你可以直接改」，并自动获得工具授权。
每轮出稿前后各取一次工作区快照，算出这次改了哪些文件，草稿上会多一个「代码改动」区块：
文件清单 + 可展开的 unified diff。

两条安全约定：

- **出稿阶段不提交**：跑 agent 的过程中不 commit、不建分支、不碰索引，改动只留在工作区。
- **跑之前脏就说明**：如果工作区原本就有未提交的修改，diff 里会标出「可能混着你自己改的内容」。

点「批准并发送」时，如果这条草稿带着改动，会先把这些文件提交到一个新分支
`gh-agent/issue-<编号>`（撞名自动加 `-2`、`-3`），再发评论。

提交是这么做的：在临时目录 `git worktree add -b` 出一个干净副本，只把这次改动的文件
拷进去提交，然后摘掉这个 worktree。所以：

- 你的当前分支不会被动，HEAD 不变；
- 工作区里那些未提交的东西不会被卷进这次提交；
- 分支只在本地，**不推送**——要不要推、推去哪，你自己决定；
- 工作区里的改动仍然保留，方便你接着调。

想验证这套逻辑：`go test ./internal/worktree ./internal/desk -v`，
其中专门有一个用例检查「用户未提交的文件不会进分支」。

想看 agent 真能改代码，可以手动跑：

```bash
ACP_EDIT=codebuddy go test ./internal/acp -run TestSmokeAgentEditsRepo -v
```

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
