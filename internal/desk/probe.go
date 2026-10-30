package desk

import (
	"context"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
)

// probeTimeout 是单次探测的上限：npx 首次拉起要下载适配器，给宽一点。
const probeTimeout = 90 * time.Second

// ProbeAgent 拉起一次 agent，读它报的模型和能力并缓存。
// refresh 为 true 时忽略缓存（界面上的「重新读取」）。
func (s *Service) ProbeAgent(id string, refresh bool) AgentProbe {
	id = strings.TrimSpace(id)
	def, ok := acp.Lookup(id)
	if !ok {
		return AgentProbe{ID: id, Detail: "不认识的 agent"}
	}

	s.mu.Lock()
	if !refresh {
		if cached, ok := s.probes[id]; ok {
			s.mu.Unlock()
			return cached
		}
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	info, err := acp.Probe(ctx, def)
	out := AgentProbe{ID: id, OK: err == nil, Info: info}
	if err != nil {
		out.Detail = err.Error()
	}

	s.mu.Lock()
	if s.probes == nil {
		s.probes = map[string]AgentProbe{}
	}
	s.probes[id] = out
	s.mu.Unlock()
	return out
}
