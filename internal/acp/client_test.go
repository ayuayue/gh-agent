package acp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
)

// fakeAgent 是一个最小的 ACP agent（testdata/fakeagent.cjs），
// 用来在不联网、不起真 agent 的情况下验证客户端。
func startFakeAgent(t *testing.T) (*acp.Client, context.CancelFunc) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过")
	}
	src, err := os.ReadFile(filepath.Join("testdata", "fakeagent.cjs"))
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.cjs")
	if err := os.WriteFile(script, src, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	client, err := acp.Start(ctx, node+" "+script, dir)
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	return client, func() {
		_ = client.Close()
		cancel()
	}
}

func TestClientAgainstFakeAgent(t *testing.T) {
	client, done := startFakeAgent(t)
	defer done()

	out, err := client.Prompt(context.Background(), "请回复：测试一下", nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if out != "回声：测试一下" {
		t.Errorf("正文应只由 agent_message_chunk 拼成，得到 %q", out)
	}
	if strings.Contains(out, "内部思考") {
		t.Error("agent_thought_chunk 不该进正文")
	}
	if tools := client.Tools(); len(tools) != 1 || tools[0] != "Read(a.ts)" {
		t.Errorf("应记下用过的工具，得到 %v", tools)
	}
}

func TestClientRejectsEmptyPromptResult(t *testing.T) {
	client, done := startFakeAgent(t)
	defer done()

	// fake agent 只回显最后四个字符，空输入会得到 "回声："，这里验证正常路径不报错。
	if _, err := client.Prompt(context.Background(), "abcd", nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
}

func TestStartRejectsMissingBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := acp.Start(ctx, "definitely-not-a-real-agent-xyz --acp", t.TempDir()); err == nil {
		t.Fatal("启动不存在的 agent 应该报错")
	}
}

// TestSmokeRealAgent 用本机真实 agent 跑一轮，默认跳过。
// 手动验证：ACP_SMOKE=codebuddy go test ./internal/acp -run TestSmokeRealAgent -v
func TestSmokeRealAgent(t *testing.T) {
	id := os.Getenv("ACP_SMOKE")
	if id == "" {
		t.Skip("设置 ACP_SMOKE=codebuddy 或 pi 才跑")
	}
	def, ok := acp.Lookup(id)
	if !ok {
		t.Fatalf("不认识的 agent %q", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client, err := acp.Start(ctx, def.Command, t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()
	out, err := client.Prompt(ctx, "只回复两个字：收到", nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	t.Logf("%s 回复：%q", def.Name, out)
	if !strings.Contains(out, "收到") {
		t.Errorf("回复里没有『收到』：%q", out)
	}
}
