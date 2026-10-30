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
	"github.com/ayuayue/gh-agent/internal/llm"
	"github.com/ayuayue/gh-agent/internal/state"
)

// llmResponder 把 llm.Client 适配成 agent.Responder，测试里只需要这一层。
type llmResponder struct{ c *llm.Client }

func (r llmResponder) Respond(ctx context.Context, req agent.Request) (string, error) {
	return r.c.Chat(ctx, req.System, req.User)
}

func (r llmResponder) Label() string { return r.c.Model }

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

	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("模型请求路径不对: %s", r.URL.Path)
		}
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("解析模型请求: %v", err)
		}
		if len(req.Messages) != 2 || !strings.Contains(req.Messages[1].Content, "@claude 解释一下这里") {
			t.Errorf("模型请求里缺少触发的评论: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"这里是解释：改在 submitAnswers。"},"finish_reason":"stop"}]}`))
	}))
	defer modelSrv.Close()

	cfg := config.Default()
	cfg.Repos = []string{"o/r"}
	cfg.MaxPerPoll = 1
	cfg.DryRun = false
	cfg.OverlapSeconds = 3600
	cfg.Signature = "签名"

	gh := ghapi.New("token")
	gh.SetBaseURL(ghSrv.URL)
	st := state.Load(filepath.Join(t.TempDir(), "state.json"))
	runner := &agent.Runner{
		Config:       cfg,
		Instructions: "你是助手",
		Me:           "me",
		Live:         true,
		GH:           gh,
		Responder:    llmResponder{llm.New(modelSrv.URL, "test-model", "", 10*time.Second, 256, 0.2)},
		State:        st,
		StatePath:    filepath.Join(t.TempDir(), "state.json"),
		Logf:         func(string, ...any) {},
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
	if !st.For("o/r").Has(2) {
		t.Error("处理过的评论应记进 state")
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

	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"答复"}}]}`))
	}))
	defer modelSrv.Close()

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
		Responder: llmResponder{llm.New(modelSrv.URL, "m", "", 10*time.Second, 128, 0)},
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
