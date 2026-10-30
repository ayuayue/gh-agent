package desk

import (
	"fmt"
	"sync"
)

// taskKey 是任务 / 锁的键：同一个条目的出稿串行，不同条目互不影响。
func taskKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// keyedLocks 是按 key（repo#number）分的互斥锁：不同条目可以同时跑，
// 同一条目串行。没人用了就把 entry 删掉，map 不会一直长。
type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedLocks) entry(key string) *keyedEntry {
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e, ok := k.m[key]
	if !ok {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	return e
}

// Lock 拿锁；同一条目正在跑就等它结束。
func (k *keyedLocks) Lock(key string) {
	k.mu.Lock()
	e := k.entry(key)
	k.mu.Unlock()
	e.mu.Lock()
}

// TryLock 拿锁，不等待。
func (k *keyedLocks) TryLock(key string) bool {
	k.mu.Lock()
	e := k.entry(key)
	k.mu.Unlock()
	if e.mu.TryLock() {
		return true
	}
	k.releaseRef(key, e)
	return false
}

// Unlock 放锁并释放引用。
func (k *keyedLocks) Unlock(key string) {
	k.mu.Lock()
	e, ok := k.m[key]
	if !ok {
		k.mu.Unlock()
		return
	}
	e.mu.Unlock()
	e.refs--
	if e.refs <= 0 {
		delete(k.m, key)
	}
	k.mu.Unlock()
}

func (k *keyedLocks) releaseRef(key string, e *keyedEntry) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if cur, ok := k.m[key]; ok && cur == e {
		e.refs--
		if e.refs <= 0 {
			delete(k.m, key)
		}
	}
}
