package agent

import "testing"

func TestRuntimeFailure(t *testing.T) {
	bad := "magpie stopped this turn before it reached the model: the agent ran the same tool call 8 times"
	if got := RuntimeFailure(bad); got == "" {
		t.Fatal("循环保护提示应被识别为运行时失败")
	}
	if got := RuntimeFailure("结论：可以合并。\n\n改动干净。"); got != "" {
		t.Errorf("正常回复不该误判，得到 %q", got)
	}
}
