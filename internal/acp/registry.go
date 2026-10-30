package acp

import (
	"os/exec"
	"strings"
)

// Agent 是一个可以经 ACP 驱动的本地编码 agent。
type Agent struct {
	ID      string
	Name    string
	Command string   // 启动命令（交给 shell 执行，参数由我们自己固定）
	Require []string // 这些可执行文件都在 PATH 上才算可用
	Desc    string
}

// Catalog 是内置支持的 agent。顺序即界面里的顺序。
var Catalog = []Agent{
	{
		ID:      "codebuddy",
		Name:    "CodeBuddy",
		Command: "codebuddy --acp --acp-transport stdio",
		Require: []string{"codebuddy"},
		Desc:    "腾讯 CodeBuddy Code，原生 ACP",
	},
	{
		ID:      "pi",
		Name:    "pi",
		Command: "npx -y pi-acp",
		Require: []string{"npx", "pi"},
		Desc:    "pi coding agent，经 pi-acp 适配器",
	},
}

// Info 是给界面看的检测结果。
type Info struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
}

// Lookup 按 id 找 agent。
func Lookup(id string) (Agent, bool) {
	for _, a := range Catalog {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// Detect 检查每个 agent 的依赖是否齐全。
func Detect() []Info {
	out := make([]Info, 0, len(Catalog))
	for _, a := range Catalog {
		missing := make([]string, 0, len(a.Require))
		for _, bin := range a.Require {
			if _, err := exec.LookPath(bin); err != nil {
				missing = append(missing, bin)
			}
		}
		info := Info{ID: a.ID, Name: a.Name, Desc: a.Desc, Available: len(missing) == 0}
		if !info.Available {
			info.Detail = "找不到 " + strings.Join(missing, ", ")
		}
		out = append(out, info)
	}
	return out
}
