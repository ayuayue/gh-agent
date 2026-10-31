package desk

import (
	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/worktree"
	"github.com/egoist/mygo"
)

// ConfigDTO 是前端看到的配置。token 不在这里：它来自 GH_TOKEN 或 gh auth。
type ConfigDTO struct {
	Repos               []string `json:"repos"`
	IntervalSeconds     int      `json:"intervalSeconds"`
	OverlapSeconds      int      `json:"overlapSeconds"`
	Trigger             string   `json:"trigger"`
	MaxPerPoll          int      `json:"maxPerPoll"`
	DryRun              bool     `json:"dryRun"`
	AllowedAssociations []string `json:"allowedAssociations"`
	Signature           string   `json:"signature"`
	// Agent 是默认出稿方：codebuddy / codex / claude / pi。
	Agent string `json:"agent"`
	// AgentPermission 是本地 agent 的工具权限：bypass / write / read / deny。
	AgentPermission string `json:"agentPermission"`
	// RepoPaths 是仓库到本地工作目录的映射，本地 agent 在这里跑才能读代码。
	RepoPaths map[string]string `json:"repoPaths"`
}

// TaskDTO 是正在出稿的那一条，界面用它显示「处理中」。
type TaskDTO struct {
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	CommentURL string `json:"commentUrl"`
	Agent      string `json:"agent"`
	StartedAt  string `json:"startedAt"`
}

// StatusDTO 是轮询的当前状态。
type StatusDTO struct {
	Running  bool   `json:"running"`
	Me       string `json:"me"`
	LastPoll string `json:"lastPoll"`
	// NextPoll 是下一轮开始时间（RFC3339），未运行或本轮还没跑完时为空。
	NextPoll   string `json:"nextPoll,omitempty"`
	LastError  string `json:"lastError"`
	Handled    int    `json:"handled"`
	Pending    int    `json:"pending"`
	ConfigPath string `json:"configPath"`
	// Tasks 是正在出稿的那些条目：界面显示「正在处理 #N（agent）已 42s」。
	Tasks []TaskDTO `json:"tasks"`
}

// Draft 是一条等着人工审核的回复。status 取 pending / posted / rejected / failed / ignored。
type Draft struct {
	ID         string `json:"id"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	IssueURL   string `json:"issueUrl"`
	CommentURL string `json:"commentUrl"`
	Author     string `json:"author"`
	Body       string `json:"body"`
	// Kind 是条目类型：issue / pr；Type 是 issue 的类别：bug / feature。
	Kind string `json:"kind"`
	Type string `json:"type,omitempty"`
	// Note 是「忽略」时写的备注。
	Note string `json:"note,omitempty"`
	// Agent 是产出这条草稿的 agent 名字。
	Agent string `json:"agent"`
	// Prompt 是当时喂给 agent 的正文，换 agent 重做时原样再用一次。
	Prompt string `json:"prompt,omitempty"`
	// Changes 是这一轮 agent 对工作区的改动，只有本地 agent 才会有。
	Changes *worktree.Result `json:"changes,omitempty"`
	// Branch / Commit 是改动落到的分支和提交。审查会在出稿后自动提交，
	// 轮询出来的草稿仍然等批准时再提交。
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	// Merge 表示 PR 审查结论是「可以合并」，批准时会先合并再回帖。
	Merge bool `json:"merge,omitempty"`
	// MergedTo 是审查后已自动合入的基准分支（比如 dev）；为空表示没合。
	MergedTo  string `json:"mergedTo,omitempty"`
	CreatedAt string `json:"createdAt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// ItemReply 是条目下已经存在的一条回复（评论），给条目列表展开时看。
type ItemReply struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	// Mine 表示这条是自己（gh-agent 账号）发的。
	Mine bool `json:"mine"`
	// Commit 是正文里提到的提交 sha，没有则为空。
	Commit string `json:"commit,omitempty"`
}

// Item 是轮询覆盖到的一个 issue / PR，条目列表按它渲染。
type Item struct {
	ID     string `json:"id"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	// Kind 是 issue / pr。
	Kind string `json:"kind"`
	// Type 是 issue 的类别：bug / feature，识别不出来时为空。
	Type string `json:"type,omitempty"`
	// Labels 是原始标签名，界面上原样展示。
	Labels    []string `json:"labels"`
	Author    string   `json:"author"`
	Comments  int      `json:"comments"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	// Replied 表示已经有自己发的回复（GitHub 上已存在，或草稿已发送）。
	Replied   bool   `json:"replied"`
	RepliedAt string `json:"repliedAt,omitempty"`
	// Replies 是已经加载出来的回复，最多保留最近几条。
	Replies []ItemReply `json:"replies"`
	// Committed 表示这个条目已经有提交：草稿提交过，或回复里带了 commit 链接。
	Committed bool   `json:"committed"`
	Commit    string `json:"commit,omitempty"`
	Branch    string `json:"branch,omitempty"`
	// Ignored 是条目级忽略；IgnoreNote 是当时写的备注。
	Ignored    bool   `json:"ignored"`
	IgnoreNote string `json:"ignoreNote,omitempty"`
	// DraftID / DraftStatus 是最近一条相关草稿，方便从列表跳回待审。
	DraftID     string `json:"draftId,omitempty"`
	DraftStatus string `json:"draftStatus,omitempty"`
}

// ItemsSnapshot 是条目列表的一次快照：加载中也会推一版，界面好显示进度。
type ItemsSnapshot struct {
	Items     []Item `json:"items"`
	Loading   bool   `json:"loading"`
	Error     string `json:"error,omitempty"`
	FetchedAt string `json:"fetchedAt,omitempty"`
	// Stale 表示这份数据来自磁盘缓存，后台正在刷新——界面照常渲染，
	// 刷完会再推一版新的。
	Stale bool `json:"stale,omitempty"`
}

// PathCheck 是本地目录的检查结果。
type PathCheck struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	IsGit  bool   `json:"isGit"`
	// Detail 是给人看的说明，ok 时为空。
	Detail string `json:"detail"`
}

// AgentInfo 是一个可选的出稿方，给界面上的下拉框用。
type AgentInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Available bool   `json:"available"`
	// Detail 说明为什么不可用。
	Detail string `json:"detail"`
	// Current 表示它是当前配置里的默认出稿方。
	Current bool `json:"current"`
}

// AgentProbe 是一次 ACP 探测的结果：默认模型、可用模型、能力。
type AgentProbe struct {
	ID     string      `json:"id"`
	OK     bool        `json:"ok"`
	Detail string      `json:"detail,omitempty"`
	Info   acp.Details `json:"info"`
}

// Notifier 弹系统通知；生产实现在 main 里，测试里可以换成空实现。
type Notifier func(title, body string)

// LogLine 是一行运行日志。
type LogLine struct {
	At    string `json:"at"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// 推给页面的事件。前端用 events.log / events.draft / events.status 订阅。
var (
	EventLog    = mygo.NewEvent[LogLine]("log")
	EventDraft  = mygo.NewEvent[Draft]("draft")
	EventStatus = mygo.NewEvent[StatusDTO]("status")
	EventItems  = mygo.NewEvent[ItemsSnapshot]("items")
)
