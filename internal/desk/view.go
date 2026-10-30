package desk

import "github.com/egoist/mygo"

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
	CreatedAt  string `json:"createdAt"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
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
