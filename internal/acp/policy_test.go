package acp_test

import (
	"testing"

	"github.com/ayuayue/gh-agent/internal/acp"
)

func TestClassifyTool(t *testing.T) {
	cases := []struct {
		kind, tool, title string
		want              acp.ToolClass
	}{
		{"read", "", "", acp.ToolRead},
		{"", "Read", "", acp.ToolRead},
		{"", "Write", "", acp.ToolWrite},
		{"", "Edit", "", acp.ToolWrite},
		{"", "MultiEdit", "", acp.ToolWrite},
		{"", "Bash", "", acp.ToolExec},
		{"", "WebFetch", "", acp.ToolNet},
		{"", "Grep", "", acp.ToolRead},
		{"", "", "Running command npm test", acp.ToolExec},
		{"", "", "Editing src/a.ts", acp.ToolWrite},
		{"", "", "读文件", acp.ToolOther},
	}
	for _, c := range cases {
		if got := acp.ClassifyTool(c.kind, c.tool, c.title); got != c.want {
			t.Errorf("ClassifyTool(%q,%q,%q) = %q, want %q", c.kind, c.tool, c.title, got, c.want)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	cases := []struct {
		policy acp.Policy
		class  acp.ToolClass
		want   bool
	}{
		{acp.PolicyBypass, acp.ToolRead, true},
		{acp.PolicyBypass, acp.ToolExec, true},
		{acp.PolicyWrite, acp.ToolRead, true},
		{acp.PolicyWrite, acp.ToolWrite, true},
		{acp.PolicyWrite, acp.ToolExec, false},
		{acp.PolicyWrite, acp.ToolNet, false},
		{acp.PolicyRead, acp.ToolRead, true},
		{acp.PolicyRead, acp.ToolWrite, false},
		{acp.PolicyRead, acp.ToolExec, false},
		{acp.PolicyDeny, acp.ToolRead, false},
	}
	for _, c := range cases {
		if got := c.policy.Allows(c.class); got != c.want {
			t.Errorf("%s 对 %s 应为 %v，实际 %v", c.policy, c.class, c.want, got)
		}
	}
}

func TestParsePolicyFallsBackToDeny(t *testing.T) {
	if got := acp.ParsePolicy(""); got != acp.PolicyDeny {
		t.Errorf("空值应退回 deny，得到 %q", got)
	}
	if got := acp.ParsePolicy("whatever"); got != acp.PolicyDeny {
		t.Errorf("认不出来的值应退回 deny，得到 %q", got)
	}
	if got := acp.ParsePolicy("  BYPASS "); got != acp.PolicyBypass {
		t.Errorf("应忽略大小写和空白，得到 %q", got)
	}
}
