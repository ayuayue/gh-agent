// Package ghapi 是 GitHub REST 的最小封装：只覆盖 gh-agent 需要的四件事
// —— 当前登录用户、按更新时间列 issue/PR、按 since 列评论、发评论。
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const apiBase = "https://api.github.com"
const apiVersion = "2022-11-28"

// User 是最小用户信息。
type User struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// Label 是 issue/PR 上的一个标签。
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Issue 同时表示 issue 和 PR（PR 带 pull_request 字段）。
type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	State       string    `json:"state"`
	User        User      `json:"user"`
	Labels      []Label   `json:"labels"`
	Comments    int       `json:"comments"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IsPull 报告这条 issue 是否是 PR。
func (i Issue) IsPull() bool { return i.PullRequest != nil }

// Comment 是 issue/PR 对话区的一条评论。
type Comment struct {
	ID                int64     `json:"id"`
	Body              string    `json:"body"`
	HTMLURL           string    `json:"html_url"`
	AuthorAssociation string    `json:"author_association"`
	User              User      `json:"user"`
	CreatedAt         time.Time `json:"created_at"`
}

// Client 是带 token 的 REST 客户端。
type Client struct {
	token string
	http  *http.Client
	base  string
}

// New 创建一个客户端；token 为空时所有请求都会 401，由调用方提前拦截。
func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 30 * time.Second}, base: apiBase}
}

// SetBaseURL 允许测试指向本地假服务。
func (c *Client) SetBaseURL(base string) { c.base = base }

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, snippet(payload))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

func snippet(raw []byte) string {
	const limit = 300
	if len(raw) > limit {
		return string(raw[:limit]) + "…"
	}
	return string(raw)
}

// CurrentLogin 返回 token 所属账号的 login。
func (c *Client) CurrentLogin(ctx context.Context) (string, error) {
	var user User
	if err := c.do(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", fmt.Errorf("github /user 未返回 login")
	}
	return user.Login, nil
}

// ListIssuesUpdatedSince 列出 since 之后更新过的 open issue 与 PR（PR 在 issues API 里同样是 issue）。
func (c *Client) ListIssuesUpdatedSince(ctx context.Context, repo string, since time.Time) ([]Issue, error) {
	query := url.Values{}
	query.Set("state", "open")
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	query.Set("per_page", "50")
	query.Set("since", since.UTC().Format(time.RFC3339))
	var issues []Issue
	path := "/repos/" + repo + "/issues?" + query.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// GetIssue 取单个 issue / PR 的详情（正文、标签、评论数）。
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (Issue, error) {
	var issue Issue
	path := "/repos/" + repo + "/issues/" + strconv.Itoa(number)
	if err := c.do(ctx, http.MethodGet, path, nil, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

// ListOpenIssues 列出仓库里全部 open 的 issue 与 PR（PR 在 issues API 里同样是 issue）。
// 条目列表页用它做一次全量拉取，不走 since。
func (c *Client) ListOpenIssues(ctx context.Context, repo string) ([]Issue, error) {
	query := url.Values{}
	query.Set("state", "open")
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	query.Set("per_page", "50")
	var issues []Issue
	path := "/repos/" + repo + "/issues?" + query.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// ListIssueComments 列出某条 issue/PR 的全部对话评论（含 PR 的普通评论）。
// 出稿时用它拿完整上下文，避免模型只看得到本轮窗口里的评论。
func (c *Client) ListIssueComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	query := url.Values{}
	query.Set("per_page", "100")
	var comments []Comment
	path := "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments?" + query.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// ListIssueCommentsSince 列出某条 issue/PR 在 since 之后的对话评论（含 PR 的普通评论）。
func (c *Client) ListIssueCommentsSince(ctx context.Context, repo string, number int, since time.Time) ([]Comment, error) {
	query := url.Values{}
	query.Set("per_page", "50")
	query.Set("since", since.UTC().Format(time.RFC3339))
	var comments []Comment
	path := "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments?" + query.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// CloseIssue 关闭 issue / PR。reason 是 GitHub 的 state_reason
// （completed / not_planned），留空只发 state=closed（PR 走这个）。
func (c *Client) CloseIssue(ctx context.Context, repo string, number int, reason string) error {
	body := map[string]any{"state": "closed"}
	if reason != "" {
		body["state_reason"] = reason
	}
	path := "/repos/" + repo + "/issues/" + strconv.Itoa(number)
	return c.do(ctx, http.MethodPatch, path, body, nil)
}

// MergePullRequest 合并一个 PR。method 取 merge / squash / rebase，空值按 merge。
func (c *Client) MergePullRequest(ctx context.Context, repo string, number int, method string) error {
	if method == "" {
		method = "merge"
	}
	path := "/repos/" + repo + "/pulls/" + strconv.Itoa(number) + "/merge"
	return c.do(ctx, http.MethodPut, path, map[string]string{"merge_method": method}, nil)
}

// PostIssueComment 发一条评论（issue 与 PR 共用同一个接口）。
func (c *Client) PostIssueComment(ctx context.Context, repo string, number int, body string) (Comment, error) {
	var created Comment
	path := "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments"
	err := c.do(ctx, http.MethodPost, path, map[string]string{"body": body}, &created)
	return created, err
}
