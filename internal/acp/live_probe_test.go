package acp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveProbe(t *testing.T) {
	list := os.Getenv("ACP_PROBE")
	if list == "" {
		t.Skip("设置 ACP_PROBE=codebuddy,claude 才跑")
	}
	for _, id := range strings.Split(list, ",") {
		def, ok := Lookup(strings.TrimSpace(id))
		if !ok {
			t.Errorf("不认识的 agent %q", id)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		info, err := Probe(ctx, def)
		cancel()
		fmt.Printf("%s => current=%q modes=%d models=%d err=%v\n", def.ID, info.CurrentModel, len(info.Modes), len(info.Models), err)
		for i, m := range info.Models {
			if i >= 6 {
				break
			}
			fmt.Printf("   model %q => %q\n", m.ID, m.Name)
		}
		for i, m := range info.Modes {
			if i >= 8 {
				break
			}
			fmt.Printf("   mode  %q => %q\n", m.ID, m.Name)
		}
	}
}

// TestLiveSetOptions 验证 session/set_model 和 session/set_mode 真能用。
func TestLiveSetOptions(t *testing.T) {
	id := os.Getenv("ACP_PROBE")
	model := os.Getenv("ACP_SET_MODEL")
	mode := os.Getenv("ACP_SET_MODE")
	if id == "" || (model == "" && mode == "") {
		t.Skip("设置 ACP_PROBE 和 ACP_SET_MODEL / ACP_SET_MODE 才跑")
	}
	def, ok := Lookup(id)
	if !ok {
		t.Fatalf("不认识的 agent %q", id)
	}
	dir, err := os.MkdirTemp("", "gh-agent-set-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client, err := Start(ctx, def.Command, dir, WithPolicy(PolicyDeny), WithModel(model), WithMode(mode))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()
	fmt.Printf("设置成功 model=%q mode=%q\n", model, mode)
}
