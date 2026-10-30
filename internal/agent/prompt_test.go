package agent

import (
	"strings"
	"testing"

	"github.com/ayuayue/gh-agent/internal/ghapi"
)

func TestBuildPromptIncludesContext(t *testing.T) {
	issue := ghapi.Issue{Number: 456, Title: "fix: 修一个问题", Body: "问题描述", HTMLURL: "https://github.com/o/r/pull/456"}
	issue.PullRequest = &struct{}{}
	out := BuildPrompt(PromptInput{
		Repo:    "o/r",
		Issue:   issue,
		Comment: comment(9, "friend", "OWNER", "@claude 这里为什么要这样改"),
		Thread: []ghapi.Comment{
			comment(1, "a", "OWNER", "第一条"),
			comment(9, "friend", "OWNER", "@claude 这里为什么要这样改"),
			comment(10, "b", "MEMBER", "补充一句"),
		},
	})
	for _, want := range []string{"o/r", "#456", "PR", "问题描述", "friend", "补充一句"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt 缺少 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "第一条") == false {
		t.Error("线程上下文应包含更早的评论")
	}
	if !strings.HasSuffix(out, "只输出回复正文。") {
		t.Error("prompt 结尾应带上输出约束")
	}
}

func TestBuildPromptTrimsThread(t *testing.T) {
	var thread []ghapi.Comment
	for i := 0; i < 12; i++ {
		thread = append(thread, comment(int64(i+1), "a", "OWNER", "历史评论"))
	}
	out := BuildPrompt(PromptInput{Repo: "o/r", Issue: ghapi.Issue{Number: 1, Title: "t"}, Comment: comment(99, "friend", "OWNER", "@claude 问"), Thread: thread})
	if got := strings.Count(out, "历史评论"); got != threadContext {
		t.Errorf("线程上下文应裁剪到 %d 条，实际 %d", threadContext, got)
	}
}

func TestFinalize(t *testing.T) {
	if got := Finalize("  ", "sig"); got != "" {
		t.Errorf("空输出应返回空串，得到 %q", got)
	}
	if got := Finalize("正文", "签名"); !strings.HasSuffix(got, "签名") {
		t.Errorf("应追加签名，得到 %q", got)
	}
	if got := Finalize("正文", ""); got != "正文" {
		t.Errorf("无签名时应原样返回，得到 %q", got)
	}
	long := strings.Repeat("字", replyMaxRunes+10)
	got := Finalize(long, "")
	if !strings.Contains(got, "已截断") {
		t.Errorf("超长输出应标注截断")
	}
}

// 模型自己发明工具调用时，宁可这条不发，也不能把标记贴进 issue。
func TestFinalizeDropsToolMarkup(t *testing.T) {
	cases := []string{
		"<｜｜DSML｜｜ calls>\n<｜｜DSML｜｜ invoke name=\"bash\">ls</｜｜DSML｜｜ invoke>\n</｜｜DSML｜｜ calls>",
		"<tool_call>{\"name\":\"bash\"}</tool_call>",
		"<function_call>read_file</function_call>",
	}
	for _, raw := range cases {
		if got := Finalize(raw, "签名"); got != "" {
			t.Errorf("纯工具调用应被丢弃，得到 %q", got)
		}
	}

	// 正文后跟工具调用：保留正文，丢掉尾巴。
	mixed := "建议先看 AcpHandshake.ts。\n<｜｜DSML｜｜ calls>\n<｜｜DSML｜｜ invoke name=\"bash\">"
	if got := Finalize(mixed, ""); got != "建议先看 AcpHandshake.ts。" {
		t.Errorf("应保留工具调用之前的正文，得到 %q", got)
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	issue := ghapi.Issue{Number: 12, Title: "拖拽后不动", Body: "详细描述", HTMLURL: "https://github.com/o/r/issues/12"}
	out := BuildReviewPrompt(PromptInput{
		Repo:  "o/r",
		Issue: issue,
		Thread: []ghapi.Comment{
			comment(1, "a", "OWNER", "我也遇到了"),
		},
		Me: "me",
	})
	for _, want := range []string{"o/r", "#12", "Issue", "详细描述", "我也遇到了", "审查这个条目", "工作区里改代码"} {
		if !strings.Contains(out, want) {
			t.Errorf("审查 prompt 缺少 %q\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "只输出要发到这条 issue / PR 的回复正文。") {
		t.Errorf("审查 prompt 结尾不对：%q", out)
	}
	if strings.Contains(out, "需要你回复的评论") {
		t.Error("审查 prompt 不该限定某一条评论")
	}
}

func TestSplitMergeVerdict(t *testing.T) {
	body, merge := SplitMergeVerdict("看起来没问题。\n\n" + MergeMarker)
	if !merge {
		t.Error("应认出合并标记")
	}
	if strings.Contains(body, "gh-agent") || !strings.HasSuffix(body, "看起来没问题。") {
		t.Errorf("标记应从正文里去掉，得到 %q", body)
	}
	if _, merge := SplitMergeVerdict("还有问题要改。"); merge {
		t.Error("没有标记时不该认为可合并")
	}
}
