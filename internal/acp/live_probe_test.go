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
		fmt.Printf("%s => %+v err=%v\n", def.ID, info, err)
	}
}
