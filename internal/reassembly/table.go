package reassembly

import (
	"bytes"
	"dtun/internal/metrics"
	"dtun/internal/proto"
	"time"
)

// Offsets fit in uint16 because the protocol caps inner packets at 9000 bytes.
type span struct{ off, n uint16 }

const spanBytes = 4

// Per-entry allowance covers entry and map/list bookkeeping. Payload and span
// backing capacities are charged separately; this is not a process RSS/GC cap.
const entryOverhead = 192

type entry struct {
	id             uint64
	buf            []byte
	spans          []span
	received, cost int
	created        time.Time
	prev, next     *entry
}

// Table is single-owner. Its FIFO tracks admission time, so expiry and pressure
// eviction don't scan the map. Closed IDs remain tombstoned in a bounded window.
type Table struct {
	entries                     map[uint64]*entry
	MaxPackets, MaxBytes, Bytes int
	Timeout                     time.Duration
	// False is retained for controlled A/B tests; production uses oldest eviction.
	EvictOldest                  bool
	high                         uint64
	closed                       [4096]uint64
	head, tail                   *entry
	M                            *metrics.Metrics
	trackedBytes, trackedPackets int
}

func New(m *metrics.Metrics) *Table {
	return &Table{entries: map[uint64]*entry{}, MaxPackets: 1024, MaxBytes: 4 << 20, Timeout: time.Second, EvictOldest: true, M: m}
}
func (t *Table) gauges() {
	t.M.Add("reassembly_memory_bytes", int64(t.Bytes-t.trackedBytes))
	t.M.Add("reassembly_pending_packets", int64(len(t.entries)-t.trackedPackets))
	t.trackedBytes, t.trackedPackets = t.Bytes, len(t.entries)
	t.M.Max("reassembly_peak_memory_bytes", int64(t.Bytes))
	t.M.Max("reassembly_peak_pending_packets", int64(len(t.entries)))
}
func (t *Table) drop(id uint64) {
	if e := t.entries[id]; e != nil {
		t.Bytes -= e.cost
		if e.prev != nil {
			e.prev.next = e.next
		} else {
			t.head = e.next
		}
		if e.next != nil {
			e.next.prev = e.prev
		} else {
			t.tail = e.prev
		}
		delete(t.entries, id)
	}
	t.closed[id%4096] = id
	t.gauges()
}
func (t *Table) Expire(now time.Time) {
	for t.head != nil && now.Sub(t.head.created) >= t.Timeout {
		t.drop(t.head.id)
		t.M.Add("reassembly_timeout", 1)
	}
}
func (t *Table) advance(id uint64) {
	if id <= t.high {
		return
	}
	delta := id - t.high
	if delta >= 4096 {
		for old := range t.entries {
			if id-old >= 4096 {
				t.drop(old)
				t.M.Add("reassembly_window_drop", 1)
			}
		}
	} else {
		for step := uint64(1); step <= delta; step++ {
			next := t.high + step
			if next >= 4096 {
				old := next - 4096
				if t.entries[old] != nil {
					t.drop(old)
					t.M.Add("reassembly_window_drop", 1)
				}
			}
		}
	}
	t.high = id
}
func (t *Table) room(cost int) bool {
	if cost > t.MaxBytes || t.MaxPackets <= 0 {
		return false
	}
	for len(t.entries) >= t.MaxPackets || t.Bytes+cost > t.MaxBytes {
		if !t.EvictOldest || t.head == nil {
			return false
		}
		t.drop(t.head.id)
		t.M.Add("reassembly_pressure_evictions", 1)
	}
	return true
}
func (t *Table) Add(f proto.Frame, now time.Time) []byte {
	if !f.Valid() || f.Type != proto.Data {
		t.M.Add("reassembly_invalid", 1)
		return nil
	}
	id := f.ID
	t.advance(id)
	if t.high-id >= 4096 || t.closed[id%4096] == id {
		return nil
	}
	t.Expire(now)
	// Expiry above can close this ID; never let a late fragment resurrect it.
	if t.closed[id%4096] == id {
		return nil
	}
	e := t.entries[id]
	if e == nil {
		// Complete datagrams need no pending slot. In particular, partial packets
		// must not starve unrelated TCP ACK/control packets when the table is full.
		if f.Offset == 0 && len(f.Payload) == f.Total {
			p := append([]byte(nil), f.Payload...)
			t.closed[id%4096] = id
			t.M.Add("reassembly_created", 1)
			t.M.Add("reassembly_completed", 1)
			return p
		}
		initial := 2
		if f.Total < initial {
			initial = f.Total
		}
		cost := entryOverhead + f.Total + initial*spanBytes
		if !t.room(cost) {
			t.closed[id%4096] = id
			t.M.Add("reassembly_limit", 1)
			return nil
		}
		e = &entry{id: id, buf: make([]byte, f.Total), spans: make([]span, 0, initial), created: now, cost: cost, prev: t.tail}
		if t.tail != nil {
			t.tail.next = e
		} else {
			t.head = e
		}
		t.tail = e
		t.entries[id] = e
		t.Bytes += cost
		t.M.Add("reassembly_created", 1)
		t.gauges()
	}
	invalid := len(e.buf) != f.Total
	if !invalid {
		for _, s := range e.spans {
			off, n := int(s.off), int(s.n)
			if off == f.Offset && n == len(f.Payload) && bytes.Equal(e.buf[off:off+n], f.Payload) {
				return nil
			}
			if f.Offset < off+n && off < f.Offset+len(f.Payload) {
				invalid = true
				break
			}
		}
	}
	if invalid {
		t.drop(id)
		t.M.Add("reassembly_invalid", 1)
		return nil
	}
	if len(e.spans) == cap(e.spans) {
		capacity := cap(e.spans) * 2
		if capacity > f.Total {
			capacity = f.Total
		}
		extra := (capacity - cap(e.spans)) * spanBytes
		if t.Bytes+extra > t.MaxBytes {
			t.drop(id)
			t.M.Add("reassembly_limit", 1)
			return nil
		}
		spans := make([]span, len(e.spans), capacity)
		copy(spans, e.spans)
		e.spans = spans
		e.cost += extra
		t.Bytes += extra
		t.gauges()
	}
	copy(e.buf[f.Offset:], f.Payload)
	e.spans = append(e.spans, span{uint16(f.Offset), uint16(len(f.Payload))})
	e.received += len(f.Payload)
	if e.received == len(e.buf) {
		p := e.buf
		t.drop(id)
		t.M.Add("reassembly_completed", 1)
		return p
	}
	return nil
}

// Release removes this table's gauges; old/new receive sessions may overlap.
func (t *Table) Release() {
	t.M.Add("reassembly_memory_bytes", -int64(t.trackedBytes))
	t.M.Add("reassembly_pending_packets", -int64(t.trackedPackets))
	t.trackedBytes, t.trackedPackets = 0, 0
}
