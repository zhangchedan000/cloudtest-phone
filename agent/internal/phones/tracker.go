package phones

import "sync"

// tracker 按设备记一份最新状态（并发安全）。
type tracker[T any] struct {
	mu     sync.Mutex
	status map[string]T
}

func (t *tracker[T]) put(id string, v T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == nil {
		t.status = map[string]T{}
	}
	t.status[id] = v
}

func (t *tracker[T]) drop(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.status, id)
}

func (t *tracker[T]) get(id string) *T {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.status[id]; ok {
		return &v
	}
	return nil
}
