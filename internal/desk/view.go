package desk

import (
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
	BaseURL             string   `json:"baseUrl"`
	Model               string   `json:"model"`
	APIKeyEnv           string   `json:"apiKeyEnv"`
	TimeoutSeconds      int      `json:"timeoutSeconds"`
	MaxOutputTokens     int      `json:"maxOutputTokens"`
	Temperature         float64  `json:"temperature"`
	// Agent 是默认出稿方：llm / codebuddy / pi。
	Agent string `json:"agent"`
	// AgentPermission 是本地 agent 的工具权限：bypass / write / read / deny。
	AgentPermission string `json:"agentPermission"`
	// RepoPaths 是仓库到本地工作目录的映射，本地 agent 在这里跑才能读代码。
	RepoPaths map[string]string `json:"repoPaths"`
}

// StatusDTO 是轮询的当前状态。
type StatusDTO struct {
	Running    bool   `json:"running"`
	Me         string `json:"me"`
	LastPoll   string `json:"lastPoll"`
	LastError  string `json:"lastError"`
	Handled    int    `json:"handled"`
	Pending    int    `json:"pending"`
	ConfigPath string `json:"configPath"`
}

// Draft 是一条等着人工审核的回复。status 取 pending / posted / rejected / failed。
type Draft struct {
	ID         string `json:"id"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	IssueURL   string `json:"issueUrl"`
	CommentURL string `json:"commentUrl"`
	Author     string `json:"author"`
	Body       string `json:"body"`
	// Agent 是产出这条草稿的 agent 名字。
	Agent string `json:"agent"`
	// Prompt 是当时喂给 agent 的正文，换 agent 重做时原样再用一次。
	Prompt string `json:"prompt,omitempty"`
	// Changes 是这一轮 agent 对工作区的改动，只有本地 agent 才会有。
	Changes *worktree.Result `json:"changes,omitempty"`
	// Branch / Commit 是批准时改动落到的分支和提交，没有改动时为空。
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	CreatedAt string `json:"createdAt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
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
)
