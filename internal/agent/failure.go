package agent

import "strings"

// runtimeFailureMarkers 是「这段文字其实是运行时错误，不是回复」的特征串。
// 有的 agent（比如 magpie 的循环保护）会把中止说明当成普通助手消息发回来，
// ACP 那边结束原因仍然是正常，不识别就会把错误当草稿发到 GitHub。
var runtimeFailureMarkers = []string{
	"stopped this turn before it reached the model",
	"the agent ran the same tool call",
	"magpie stopped this turn",
}

// RuntimeFailure 检查正文是不是运行时失败提示；是就返回命中的那一行，否则返回空串。
// 按行匹配，只认明确的中止措辞，避免把正常回复里顺带提到这些词也误判。
func RuntimeFailure(text string) string {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		for _, marker := range runtimeFailureMarkers {
			if strings.Contains(lower, marker) {
				return strings.TrimSpace(line)
			}
		}
	}
	return ""
}
