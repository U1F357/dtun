package metrics

import (
	"encoding/json"
	"os"
	"runtime"
	"sync"
)

type Metrics struct {
	mu     sync.Mutex
	values map[string]int64
}

func New() *Metrics                      { return &Metrics{values: map[string]int64{}} }
func (m *Metrics) Add(k string, n int64) { m.mu.Lock(); m.values[k] += n; m.mu.Unlock() }
func (m *Metrics) Set(k string, n int64) { m.mu.Lock(); m.values[k] = n; m.mu.Unlock() }
func (m *Metrics) Max(k string, n int64) {
	m.mu.Lock()
	if n > m.values[k] {
		m.values[k] = n
	}
	m.mu.Unlock()
}
func (m *Metrics) JSON() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.values)
	return string(b)
}

// Runtime records process resource gauges for soak/reconnect validation.
func (m *Metrics) Runtime() {
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	m.Set("runtime_goroutines", int64(runtime.NumGoroutine()))
	m.Set("runtime_heap_alloc_bytes", int64(s.HeapAlloc))
	m.Set("runtime_heap_sys_bytes", int64(s.HeapSys))
	m.Set("runtime_gc_cycles", int64(s.NumGC))
	if fds, e := os.ReadDir("/proc/self/fd"); e == nil {
		m.Set("runtime_open_fds", int64(len(fds)))
	}
}
