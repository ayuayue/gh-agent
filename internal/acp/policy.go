package acp

import "strings"

// ToolClass 是工具的大类，权限策略按这个粒度放行。
type ToolClass string

const (
	ToolRead  ToolClass = "read"   // 读文件、搜索、列目录
	ToolWrite ToolClass = "write"  // 改文件、新建、删除、移动
	ToolExec  ToolClass = "exec"   // 跑命令、开终端
	ToolNet   ToolClass = "net"    // 联网抓取
	ToolOther ToolClass = "other"  // 认不出来的
)

// Policy 决定客户端怎么答复 agent 的工具授权请求。
//
// 注意：只能约束"会来问"的 agent。有的适配器（比如 pi-acp）自己就把工具放行了，
// 根本不会发 session/request_permission，这种情况下策略无从施加，
// 只能靠出稿后的 diff 让人看见它改了什么。
type Policy string

const (
	// PolicyBypass 全部放行：读、改、跑命令、联网都不问。
	PolicyBypass Policy = "bypass"
	// PolicyWrite 允许读和改文件，拒绝执行命令和联网。
	PolicyWrite Policy = "write"
	// PolicyRead 只允许读，拒绝改文件和执行。
	PolicyRead Policy = "read"
	// PolicyDeny 全部拒绝，agent 只能靠 prompt 里的上下文作答。
	PolicyDeny Policy = "deny"
)

// ParsePolicy 解析配置里的取值，认不出来的一律退回 deny（宁紧勿松）。
func ParsePolicy(s string) Policy {
	switch Policy(strings.TrimSpace(strings.ToLower(s))) {
	case PolicyBypass:
		return PolicyBypass
	case PolicyWrite:
		return PolicyWrite
	case PolicyRead:
		return PolicyRead
	default:
		return PolicyDeny
	}
}

// Allows 判断这类工具在当前策略下能不能放行。
func (p Policy) Allows(class ToolClass) bool {
	switch p {
	case PolicyBypass:
		return true
	case PolicyWrite:
		return class == ToolRead || class == ToolWrite
	case PolicyRead:
		return class == ToolRead
	default:
		return false
	}
}

// Label 是给界面看的说明。
func (p Policy) Label() string {
	switch p {
	case PolicyBypass:
		return "全部放行（读、改文件、跑命令、联网）"
	case PolicyWrite:
		return "允许读和改文件，禁止执行命令与联网"
	case PolicyRead:
		return "只读，禁止改文件和执行命令"
	default:
		return "全部拒绝，只靠 prompt 里的上下文"
	}
}

// ClassifyTool 把一次工具调用归到大类。
//
// ACP 标准是 toolCall.kind；但 codebuddy 只给 _meta["codebuddy.ai/toolName"]，
// 所以三个来源都要看：kind、meta 里的工具名、以及标题兜底。
func ClassifyTool(kind, toolName, title string) ToolClass {
	for _, raw := range []string{kind, toolName, title} {
		if class, ok := classOf(raw); ok {
			return class
		}
	}
	return ToolOther
}

func classOf(raw string) (ToolClass, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return ToolOther, false
	}
	switch {
	case strings.Contains(name, "bash"), strings.Contains(name, "shell"),
		strings.Contains(name, "terminal"), strings.Contains(name, "execute"),
		strings.Contains(name, "command"), strings.Contains(name, "exec"):
		return ToolExec, true
	case strings.Contains(name, "write"), strings.Contains(name, "edit"),
		strings.Contains(name, "delete"), strings.Contains(name, "remove"),
		strings.Contains(name, "move"), strings.Contains(name, "rename"),
		strings.Contains(name, "patch"), strings.Contains(name, "notebook"),
		strings.Contains(name, "apply"):
		return ToolWrite, true
	case strings.Contains(name, "fetch"), strings.Contains(name, "web"),
		strings.Contains(name, "http"), strings.Contains(name, "browse"):
		return ToolNet, true
	case strings.Contains(name, "read"), strings.Contains(name, "grep"),
		strings.Contains(name, "glob"), strings.Contains(name, "search"),
		strings.Contains(name, "list"), strings.Contains(name, "ls"),
		strings.Contains(name, "think"), strings.Contains(name, "view"):
		return ToolRead, true
	}
	return ToolOther, false
}
