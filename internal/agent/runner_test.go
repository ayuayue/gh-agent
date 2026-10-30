package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/state"
)

// stubResponder 是测试用的出稿方：记下收到的 prompt，回一段固定正文。
type stubResponder struct {
	prompt string
	reply  string
}

func (r *stubResponder) Respond(_ context.Context, req agent.Request) (agent.Reply, error) {
	r.prompt = req.User
	return agent.Reply{Text: r.reply}, nil
}

func (r *stubResponder) Label() string { return "stub" }

// TestRunnerEndToEnd 用两个假服务（GitHub REST + OpenAI 兼容端点）跑通
// 「列条目 → 列评论 → 过滤 → 出稿 → 回帖 → 记账」整条链路，不碰外网。
func TestRunnerEndToEnd(t *testing.T) {
	var posted string
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			writeJSON(t, w, map[string]any{"login": "me", "type": "User"})
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/") && strings.HasSuffix(r.URL.Path, "/comments") && r.Method == http.MethodPost:
			var payload struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("解析回帖体: %v", err)
			}
			posted = payload.Body
			writeJSON(t, w, map[string]any{"id": 999, "body": payload.Body})
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/") && strings.HasSuffix(r.URL.Path, "/comments"):
			writeJSON(t, w, []map[string]any{
				{"id": 1, "body": "普通评论", "author_association": "OWNER", "user": map[string]string{"login": "friend", "type": "User"}},
				{"id": 2, "body": "请 @claude 解释一下这里", "author_association": "OWNER", "user": map[string]string{"login": "friend", "type": "User"}, "created_at": time.Now().UTC().Format(time.RFC3339)},
			})
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/issues"):
			now := time.Now().UTC().Format(time.RFC3339)
			writeJSON(t, w, []map[string]any{{
				"number": 7, "title": "有问题的改动", "body": "描述", "state": "open",
				"user": map[string]string{"login": "friend", "type": "User"}, "created_at": now, "updated_at": now,
			}})
		default:
			t.Errorf("意外的 GitHub 请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ghSrv.Close()

	responder := &stubResponder{reply: "这里是解释：改在 submitAnswers。"}

	cfg := config.Default()
	cfg.Repos = []string{"o/r"}
	cfg.MaxPerPoll = 1
	cfg.DryRun = false
	cfg.OverlapSeconds = 3600
	cfg.Signature = "签名"

	gh := ghapi.New("token")
	gh.SetBaseURL(ghSrv.URL)
	st := state.Load(filepath.Join(t.TempDir(), "state.json"))
	var tasks []*agent.Task
	runner := &agent.Runner{
		Config:       cfg,
		Instructions: "你是助手",
		Me:           "me",
		Live:         true,
		GH:           gh,
		Responder:    responder,
		State:        st,
		StatePath:    filepath.Join(t.TempDir(), "state.json"),
		Logf:         func(string, ...any) {},
		OnTask:       func(task *agent.Task) { tasks = append(tasks, task) },
	}

	handled, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handled != 1 {
		t.Fatalf("应处理 1 条，实际 %d", handled)
	}
	if !strings.Contains(posted, "这里是解释") || !strings.HasSuffix(posted, "签名") {
		t.Errorf("回帖内容不对: %q", posted)
	}
	if !strings.Contains(responder.prompt, "@claude 解释一下这里") {
		t.Errorf("prompt 里缺少触发的评论：%s", responder.prompt)
	}
	if !st.For("o/r").Has(2) {
		t.Error("处理过的评论应记进 state")
	}
	// 出稿开始上报一条任务，结束时再报一条 Done：界面靠它显示「处理中」。
	if len(tasks) != 2 || tasks[0] == nil || tasks[1] == nil {
		t.Fatalf("OnTask 应收到「开始 + 结束」两次回调，实际 %+v", tasks)
	}
	if tasks[0].Done {
		t.Error("第一条应是开始事件")
	}
	if !tasks[1].Done || tasks[1].Repo != "o/r" || tasks[1].Number != 7 {
		t.Errorf("结束事件应带上同一个条目：%+v", tasks[1])
	}
	if tasks[0].Repo != "o/r" || tasks[0].Number != 7 || tasks[0].Agent == "" {
		t.Errorf("任务信息不完整：%+v", tasks[0])
	}
	if tasks[0].StartedAt.IsZero() {
		t.Error("任务应带上开始时间")
	}
	if tasks[0].Cancel == nil {
		t.Error("任务应带可取消的 context，界面上才能停止")
	}
}

// TestRunnerDryRunSkipsPosting 保证 dry-run 绝不写 GitHub。
func TestRunnerDryRunSkipsPosting(t *testing.T) {
	posted := false
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			writeJSON(t, w, map[string]any{"login": "me"})
		case r.Method == http.MethodPost:
			posted = true
			writeJSON(t, w, map[string]any{"id": 1})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			writeJSON(t, w, []map[string]any{{"id": 5, "body": "@claude 问", "author_association": "OWNER", "user": map[string]string{"login": "friend"}}})
		default:
			now := time.Now().UTC().Format(time.RFC3339)
			writeJSON(t, w, []map[string]any{{"number": 1, "title": "t", "state": "open", "created_at": now, "updated_at": now}})
		}
	}))
	defer ghSrv.Close()

	cfg := config.Default()
	cfg.Repos = []string{"o/r"}
	cfg.MaxPerPoll = 1
	cfg.DryRun = true
	cfg.OverlapSeconds = 3600

	gh := ghapi.New("token")
	gh.SetBaseURL(ghSrv.URL)
	statePath := filepath.Join(t.TempDir(), "state.json")
	runner := &agent.Runner{
		Config: cfg, Instructions: "x", Me: "me", Live: false, GH: gh,
		Responder: &stubResponder{reply: "答复"},
		State:     state.Load(statePath),
		StatePath: statePath,
		Logf:      func(string, ...any) {},
	}
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if posted {
		t.Error("dry-run 不应回帖")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("写 JSON: %v", err)
	}
}

// noopResponder 在「不该出稿」的用例里兜底：真被调用就说明过滤没生效。
type noopResponder struct{ t *testing.T }

func (r noopResponder) Respond(context.Context, agent.Request) (agent.Reply, error) {
	r.t.Error("被忽略的条目 / 评论不该触发模型")
	return agent.Reply{Text: "不该出现"}, nil
}

func (r noopResponder) Label() string { return "noop" }

// TestRunnerSkipsIgnored 验证手动忽略：条目级直接跳过，评论级不进模型。
func TestRunnerSkipsIgnored(t *testing.T) {
	commentCalls := 0
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			writeJSON(t, w, map[string]any{"login": "me"})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			commentCalls++
			writeJSON(t, w, []map[string]any{{
				"id": 5, "body": "@claude 问", "author_association": "OWNER",
				"user": map[string]string{"login": "friend"},
			}})
		default:
			now := time.Now().UTC().Format(time.RFC3339)
			writeJSON(t, w, []map[string]any{{
				"number": 1, "title": "t", "state": "open", "created_at": now, "updated_at": now,
			}})
		}
	}))
	defer ghSrv.Close()

	cfg := config.Default()
	cfg.Repos = []string{"o/r"}
	cfg.MaxPerPoll = 1
	cfg.OverlapSeconds = 3600

	gh := ghapi.New("token")
	gh.SetBaseURL(ghSrv.URL)
	statePath := filepath.Join(t.TempDir(), "state.json")
	st := state.Load(statePath)
	runner := &agent.Runner{
		Config: cfg, Instructions: "x", Me: "me", Live: true, GH: gh,
		Responder: noopResponder{t: t}, State: st, StatePath: statePath,
		Logf: func(string, ...any) {},
	}

	st.For("o/r").IgnoreIssue(1, "看过了")
	if handled, err := runner.RunOnce(context.Background()); err != nil || handled != 0 {
		t.Fatalf("忽略整个条目后不该处理，handled=%d err=%v", handled, err)
	}
	if commentCalls != 0 {
		t.Error("条目被忽略时不该去拉评论")
	}

	st.For("o/r").UnignoreIssue(1)
	st.For("o/r").IgnoreComment(5, "重复")
	if handled, err := runner.RunOnce(context.Background()); err != nil || handled != 0 {
		t.Fatalf("忽略评论后不该处理，handled=%d err=%v", handled, err)
	}
	if commentCalls == 0 {
		t.Error("取消条目忽略后应该正常拉评论")
	}
}
