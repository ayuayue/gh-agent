# gh-agent

本机轮询 GitHub，把 issue / PR 交给**本机 agent**（CodeBuddy / Codex / Claude Code / pi）
分析、改代码、出稿并回帖的小工具。

不依赖 GitHub Actions、不开公网端口、不装 GitHub App：只做**出站**请求
（读 GitHub REST + 调本机 agent），跑在你自己机器上。

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

窗口四块：**待审**（草稿 + markdown 预览 + 批准/驳回/忽略）、**条目**（issue / PR
列表与已有回复）、**事件流**（轮询日志）、**设置**（仓库、间隔、触发词、出稿方）。
关掉窗口不退出，点托盘图标再开。

草稿落盘在 `%AppData%/gh-agent/drafts.json`，重启不丢。有草稿进「待审」时弹系统
通知（同组通知会合并成一条），点通知回到窗口；应用日志在 `app.log`，排查退出问题用。
窗口不带原生菜单栏（Edit / View / Window 那排）；输入框里的复制粘贴由 WebView2 自己处理，
开始 / 停止 / 退出都在托盘菜单里。

> 桌面壳永远走人工审核，不会自己发帖。要全自动，用 CLI 的 `--live`。

启动应用就会自动开始轮询（托盘菜单也能手动开始 / 停止），每 `interval_seconds` 跑一轮：
列出 since 之后有更新的 issue / PR → 拉评论 → 过触发规则 → 交给本地 agent 出稿 →
进「待审」。命中之后窗口顶部会出现「正在处理 repo#N · agent · 已 42s」，右边能直接
**停止**这一条；事件流里还会打出 `CodeBuddy 已启动（pid 1234）`，方便对上进程。

### 条目列表

「条目」页签按需从配置的仓库拉一次 open 的 issue / PR，按 Issue 和 PR 分组：

- Issue 会标出 Bug / Feature：先看标签，没有标签再看标题里的 `[Bug]`、`bug:`、`需求`；
- **已回复**：GitHub 上已经有自己发的评论，或本地草稿已发送；
- **已提交**：草稿批准时提交过分支，或回复正文里带了 `/commit/<sha>` 链接；
- 展开能看到已经加载出来的回复（最近 8 条），提到 commit 的会单独标出来。

列表不跟着轮询自动刷新——点「刷新」或批准/发送之后才重新拉，省 GitHub 配额。

每行右边的「审查」是手动触发，不用等轮询。agent 按条目类型自己决定做到哪一步：

- **Bug**：先定位根因、改代码；正文或评论里已有方案就先评估，可行按它做，不可行说明原因；
- **Feature**：改动小、好验证的直接实现；影响面大的只给方案、不动代码；
- **PR**：看改动判断能不能合，没问题会在回复里带一个隐藏的合并标记。

审查跑在一个**独立的 git worktree** 里（分支 `gh-agent/issue-<编号>`，基于 `dev`），
agent 改文件、切分支都碰不到你正在用的工作区；跑完自动提交（提交信息带 `[gh-agent]` 和
`Auto-Committed-By: gh-agent`），摘掉 worktree，然后：

- 如果你的主仓库正停在 `dev` 且**工作区干净**，改动会 `--no-ff` 合回 `dev`；
- 否则不合，改动留在分支上（草稿里会写明没合的原因），你自己合。

草稿进「待审」，批准只负责发回复；PR 带合并标记的，批准会先合并 PR 再回帖。

锁按条目分：不同条目可以同时审查，同一条目串行。窗口顶部的任务条每条都有「停止」，
停掉的这条评论不会在下一轮自动重试。

### 忽略

「忽略」分两级，都可以写一句备注：

- **草稿上忽略**：只跳过这一条评论，之后轮询不再处理它；
- **条目上忽略**：整条 issue / PR 都不再处理，它下面还在待审的草稿会一起变成「已忽略」。

忽略只影响之后的轮询，不改动 GitHub；历史里点「恢复」、条目上点「取消忽略」可以撤销。
忽略记录跟着已处理 id 一起写在 `state.json` 里。

## 出稿方：四个本地 agent 里选一个

配置里的 `agent` 决定谁来写这条回复（默认没选，必须选一个）：

| 值 | 怎么跑 | 说明 |
|---|---|---|
| `codebuddy` | `codebuddy --acp --acp-transport stdio` | 腾讯 CodeBuddy Code，原生 ACP |
| `codex` | `npx -y @zed-industries/codex-acp` | OpenAI Codex CLI，经 codex-acp 适配器 |
| `claude` | `npx -y @zed-industries/claude-code-acp` | Claude Code，经 claude-code-acp 适配器 |
| `pi` | `npx -y pi-acp` | pi coding agent，经 pi-acp 适配器 |

四个都跑在本机进程里，默认都能读代码、跑命令。设置页每个 agent 卡片的「读取参数」会
真起一次进程，通过 ACP 的 `initialize` / `session/new` 读出名字、版本、默认模型、可用模型、
模式和能力，显示在卡片下面（服务端有缓存，不会每次翻设置都重起）。

读出参数之后，可以在「模型」「模式」两个下拉里改：模型走 `session/set_model`，
模式走 `session/set_mode`（CodeBuddy 的 PTC / 权限模式就在这），保存后**下一次新起的会话**
生效。参数按 agent 分别记在配置里：

```json
"agent_options": {
  "codebuddy": { "model": "custom-local:codebuddy/glm-5.3", "mode": "acceptEdits" }
}
```

不填就用 agent 自己的默认。选错模型 / 模式时启动会直接报错，不会悄悄退回默认。

本地 agent 走 **ACP（Agent Client Protocol）**，跟编辑器接 agent 是同一套协议。
ACP 官方只提供 Kotlin / Python / Rust / TypeScript 的 SDK，没有 Go 版，
所以 `internal/acp` 是按官方 v1 schema 自己实现的客户端（JSON-RPC over stdio，
只用到 `initialize` / `session/new` / `session/prompt` / `session/update` 四个方法）。

agent 能用哪些工具由 `agent_permission` 控制：

| 值 | 允许 |
|---|---|
| `bypass`（默认） | 读文件、改代码、跑命令、联网，全部放行 |
| `write` | 允许读和改文件，禁止执行命令与联网 |
| `read` | 只读 |
| `deny` | 全拒，agent 只能靠 prompt 里的上下文回答 |

只能约束**会主动询问**的 agent：CodeBuddy 每次工具调用都发 `session/request_permission`，
拦得住；pi-acp 内部直接放行，设成只读也拦不住它改文件——那种情况只能靠出稿后的 diff 看。

agent 要读代码，得先告诉它仓库在本地哪：

```json
"agent": "codebuddy",
"repo_paths": { "ayuayue/PiDeck": "D:/project/pideck" }
```

可以映射任意多个仓库，一个仓库对应一个本地目录，互相独立。**配了映射的仓库会自动加入
监控列表，不用在 `repos` 里再写一遍**——设置页里两者就是同一张表（仓库 + 可选目录）。
界面上会检查目录：
不存在、不是目录、不是 git 仓库都会标出来（不是 git 仓库时 agent 照样能改文件，
但改动记录不下来，也就没法在批准时提交到分支）。

没填目录的仓库照样会被监控，只是本地 agent 会在配置目录下开一个空的 `work/<repo>`，
读不到代码，只能靠 prompt 里的上下文回答。

界面上每条草稿都能「换 agent 重做」：同一个 prompt 换一个 agent 再出一遍，方便对比；
也可以「编辑」就地改正文再发。

### agent 改代码

工作目录是 git 仓库时，agent 会被告知「你可以直接改」，工具授权按上面的 `agent_permission` 决定。
每轮出稿前后各取一次工作区快照，算出这次改了哪些文件，草稿上会多一个「代码改动」区块：
文件清单 + 可展开的 unified diff。

提交时机分两种：

- **轮询出的草稿**：出稿阶段不提交，点「批准并发送」时才提交到分支再回帖；
- **手动「审查」**：agent 跑完就自动提交（提交信息带 `[gh-agent]` 和
  `Auto-Committed-By: gh-agent`），批准只负责回帖；PR 带合并标记的，批准会先合并再回帖。

两种都会在提交前提醒：如果工作区原本就有未提交的修改，diff 里会标出「可能混着你自己改的内容」。

提交到的是新分支 `gh-agent/issue-<编号>`（撞名自动加 `-2`、`-3`）。

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

# 2. 改配置：仓库、出稿方（codebuddy / codex / claude / pi）、触发词

# 3. 干跑一轮（只打印，不发评论）
go run ./cmd/gh-agent --once

# 4. 真发
go run ./cmd/gh-agent --once --live

# 5. 常驻
go run ./cmd/gh-agent
```

Token 解析顺序：`GH_TOKEN` → `GITHUB_TOKEN` → `gh auth token`（复用已登录的 gh，不落盘）。

## 触发规则

`trigger` 有值时走「评论」模式，一条评论同时满足才会被处理：

1. 作者不是自己（按 login）、不是 bot；
2. `author_association` 在允许列表（默认 OWNER / MEMBER / COLLABORATOR / CONTRIBUTOR）；
3. 正文命中 `trigger`（大小写不敏感）；
4. 这条 comment id 没处理过（state 里记最近 1000 条）。

`trigger` **留空**时走「逐条审查」模式，不再看评论：

- 扫所有 open 的 issue / PR，**只处理还没有任何评论的**；
- 有评论的跳过——说明人已经表过态（通常修完才评论，或决定不改）；
- 审过的条目记在 `state.json` 的 `seen_issues` 里，不会重复审；
- 每轮最多 `max_per_poll` 条。

审查模式和条目页的「审查」按钮走同一套流程：能改代码就改、自动提交、进待审等你批准。

## 目录

```
main.go              桌面应用入口（托盘 + 窗口 + Bind）
index.html, src/     前端（React + Vite），src/mygo.ts 是 mygo generate 生成的类型化客户端
mygo.config.ts       应用名、标识、打包方式
cmd/gh-agent/        CLI 入口与参数
internal/bootstrap/  装配：token → GitHub 客户端 → 模型客户端 → Runner（CLI 和桌面共用）
internal/desk/       桌面服务层：配置读写、轮询控制、待审队列、条目列表、事件推送
internal/config/     配置读写与校验
internal/ghapi/      GitHub REST 最小封装（issue / comment / 回帖）
internal/agent/      过滤规则、prompt 组装、轮询器
internal/state/      游标与已处理 id（按仓库分开）
```

## 工具链

MyGo 要求 **Go 1.27.3+**。本仓库 `go.mod` 已声明 `go 1.27.3`，
Go 的 `GOTOOLCHAIN=auto` 会自动下载对应工具链，**不需要动系统里装的 Go**。

## 已知不做

- 不自动合并 PR：只有「审查」结论带合并标记、**并且你点了批准**的 PR 才会合并。
- 不推送分支：自动提交只留在本地，推不推、推去哪你自己决定。
- 不开新 PR。
- 不处理 GitHub Discussions（需要 GraphQL，等有需求再说）。
- PR 行内评论（review comments）暂不支持。
