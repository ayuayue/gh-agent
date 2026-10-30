package desk

import (
	"testing"
	"time"
)

func TestKeyedLocksPerKey(t *testing.T) {
	var k keyedLocks
	if !k.TryLock("o/r#1") {
		t.Fatal("第一次应拿到锁")
	}
	if k.TryLock("o/r#1") {
		t.Error("同一个条目第二次应失败")
	}
	if !k.TryLock("o/r#2") {
		t.Error("不同条目应能同时跑")
	}
	k.Unlock("o/r#1")
	k.Unlock("o/r#2")
	if !k.TryLock("o/r#1") {
		t.Error("放锁后应能再拿")
	}
	k.Unlock("o/r#1")
}

func TestKeyedLocksWaits(t *testing.T) {
	var k keyedLocks
	if !k.TryLock("o/r#1") {
		t.Fatal("先占住锁")
	}
	done := make(chan struct{})
	go func() {
		k.Lock("o/r#1")
		close(done)
		k.Unlock("o/r#1")
	}()

	select {
	case <-done:
		t.Fatal("同一条目不该立刻拿到锁")
	case <-time.After(50 * time.Millisecond):
	}

	k.Unlock("o/r#1")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("放锁后应唤醒等待者")
	}
}
