package reassembly

import (
	"bytes"
	"dtun/internal/metrics"
	"dtun/internal/proto"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func pieces(id uint64) []proto.Frame {
	var fs []proto.Frame
	_ = proto.Fragment(id, bytes.Repeat([]byte{7}, 1500), 500, func(b []byte) error { f, _ := proto.Decode(b); fs = append(fs, f); return nil })
	return fs
}
func TestOrderDuplicate(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		tab := New(metrics.New())
		fs := pieces(1)
		rand.New(rand.NewSource(seed)).Shuffle(3, func(i, j int) { fs[i], fs[j] = fs[j], fs[i] })
		count := 0
		for _, f := range append(fs, fs...) {
			if p := tab.Add(f, time.Now()); p != nil {
				count++
				if !bytes.Equal(p, bytes.Repeat([]byte{7}, 1500)) {
					t.Fatal("payload")
				}
			}
		}
		if count != 1 || tab.Bytes != 0 {
			t.Fatal(count, tab.Bytes)
		}
	}
}
func TestLossAndExpiry(t *testing.T) {
	tab := New(metrics.New())
	now := time.Now()
	fs := pieces(1)
	tab.Add(fs[0], now)
	tab.Add(fs[2], now)
	tab.Expire(now.Add(time.Second))
	if tab.Bytes != 0 {
		t.Fatal(tab.Bytes)
	}
	if p := tab.Add(fs[1], now.Add(2*time.Second)); p != nil || len(tab.entries) != 0 {
		t.Fatal("late resurrection")
	}
}
func TestOverlapAndConflict(t *testing.T) {
	for _, f := range []proto.Frame{{Type: 1, ID: 1, Total: 1499, Offset: 500, Payload: []byte{3}}, {Type: 1, ID: 1, Total: 1500, Offset: 499, Payload: []byte{3, 4}}, {Type: 1, ID: 1, Total: 1500, Offset: 0, Payload: bytes.Repeat([]byte{8}, 500)}} {
		tab := New(metrics.New())
		tab.Add(pieces(1)[0], time.Now())
		tab.Add(f, time.Now())
		if tab.Bytes != 0 {
			t.Fatal("invalid packet retained")
		}
		for _, v := range pieces(1) {
			if tab.Add(v, time.Now()) != nil {
				t.Fatal("resurrected")
			}
		}
	}
}
func TestLimits(t *testing.T) {
	tab := New(metrics.New())
	tab.MaxPackets = 1
	tab.Add(pieces(1)[0], time.Now())
	tab.Add(pieces(2)[0], time.Now())
	if len(tab.entries) != 1 {
		t.Fatal("count")
	}
	tab = New(metrics.New())
	tab.MaxBytes = 1
	tab.Add(pieces(1)[0], time.Now())
	if tab.Bytes != 0 {
		t.Fatal("bytes")
	}
	tab = New(metrics.New())
	for id := uint64(1); id < 10000; id++ {
		tab.Add(pieces(id)[0], time.Now())
	}
	if len(tab.entries) > 1024 || tab.Bytes > tab.MaxBytes {
		t.Fatal("unbounded")
	}
}
func FuzzReassembly(f *testing.F) {
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, b []byte) {
		tab := New(metrics.New())
		for i := 0; i+4 < len(b) && i < 10000; i += 5 {
			id := uint64(b[i]) + 1
			total := int(b[i+1]) + 1
			off := int(b[i+2])
			n := int(b[i+3]) + 1
			tab.Add(proto.Frame{Type: 1, ID: id, Total: total, Offset: off, Payload: bytes.Repeat([]byte{b[i+4]}, n)}, time.Unix(int64(i/500), 0))
		}
		if tab.Bytes > tab.MaxBytes || len(tab.entries) > tab.MaxPackets {
			t.Fatal("limit")
		}
	})
}

func TestCompactCapacity(t *testing.T) {
	tab := New(metrics.New())
	now := time.Now()
	for id := uint64(1); id <= 1024; id++ {
		tab.Add(pieces(id)[0], now)
	}
	if len(tab.entries) != 1024 || tab.Bytes > 2<<20 {
		t.Fatalf("slots=%d bytes=%d", len(tab.entries), tab.Bytes)
	}
}
func TestOldestEvictionAndTombstone(t *testing.T) {
	tab := New(metrics.New())
	tab.MaxPackets = 2
	now := time.Now()
	tab.Add(pieces(1)[0], now)
	tab.Add(pieces(3)[0], now.Add(time.Millisecond))
	tab.Add(pieces(2)[0], now.Add(2*time.Millisecond))
	if tab.entries[1] != nil || tab.entries[3] == nil || tab.entries[2] == nil {
		t.Fatal("not oldest admission")
	}
	for _, f := range pieces(1) {
		if tab.Add(f, now.Add(3*time.Millisecond)) != nil {
			t.Fatal("evicted ID resurrected")
		}
	}
	for _, f := range pieces(2)[1:] {
		tab.Add(f, now.Add(4*time.Millisecond))
	}
	if tab.entries[2] != nil || tab.entries[3] == nil {
		t.Fatal("new packet could not complete")
	}
	tab.Expire(now.Add(2 * time.Second))
	if tab.Bytes != 0 || tab.head != nil || tab.tail != nil {
		t.Fatal("list/accounting leak")
	}
}
func TestCompletePacketBypassesFullTable(t *testing.T) {
	tab := New(metrics.New())
	tab.MaxPackets = 1
	now := time.Now()
	tab.Add(pieces(1)[0], now)
	f := proto.Frame{Type: 1, ID: 2, Total: 3, Payload: []byte{1, 2, 3}}
	if p := tab.Add(f, now); !bytes.Equal(p, f.Payload) {
		t.Fatal("complete packet starved")
	}
	if tab.entries[1] == nil {
		t.Fatal("unnecessary eviction")
	}
	if tab.Add(f, now) != nil {
		t.Fatal("duplicate")
	}
}
func TestManyFragmentsAndGrowthBudget(t *testing.T) {
	tab := New(metrics.New())
	now := time.Now()
	for off := 1499; off >= 0; off-- {
		p := tab.Add(proto.Frame{Type: 1, ID: 1, Total: 1500, Offset: off, Payload: []byte{byte(off)}}, now)
		if off == 0 {
			if len(p) != 1500 {
				t.Fatal("missing reassembly")
			}
			for i := range p {
				if p[i] != byte(i) {
					t.Fatal("corrupt")
				}
			}
		}
	}
	if tab.Bytes != 0 {
		t.Fatal("accounting leak")
	}
	tab = New(metrics.New())
	tab.MaxBytes = entryOverhead + 10 + 2*spanBytes
	for off := 0; off < 3; off++ {
		tab.Add(proto.Frame{Type: 1, ID: 1, Total: 10, Offset: off, Payload: []byte{1}}, now)
	}
	if tab.Bytes != 0 || len(tab.entries) != 0 {
		t.Fatal("growth exceeded budget")
	}
}
func TestWindowAdvanceAndWrapEdge(t *testing.T) {
	tab := New(metrics.New())
	now := time.Now()
	tab.Add(pieces(1)[0], now)
	tab.Add(pieces(4097)[0], now)
	if tab.entries[1] != nil {
		t.Fatal("old slot")
	}
	tab.Add(pieces(^uint64(0) - 1)[0], now)
	tab.Add(pieces(^uint64(0))[0], now)
	tab.Expire(now.Add(2 * time.Second))
	if tab.Bytes != 0 {
		t.Fatal("leak")
	}
}
func BenchmarkReassembly1500(b *testing.B) {
	p := bytes.Repeat([]byte{7}, 1500)
	tab := New(metrics.New())
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint64(i + 1)
		tab.Add(proto.Frame{Type: 1, ID: id, Total: 1500, Payload: p[:1100]}, now)
		tab.Add(proto.Frame{Type: 1, ID: id, Total: 1500, Offset: 1100, Payload: p[1100:]}, now)
	}
}

func assertState(t *testing.T, tab *Table) {
	t.Helper()
	sum, count := 0, 0
	var prev *entry
	for e := tab.head; e != nil; e = e.next {
		count++
		if count > len(tab.entries) {
			t.Fatal("list cycle")
		}
		if e.prev != prev || tab.entries[e.id] != e {
			t.Fatal("list/map mismatch")
		}
		if e.cost != entryOverhead+len(e.buf)+cap(e.spans)*spanBytes {
			t.Fatal("entry accounting")
		}
		sum += e.cost
		prev = e
	}
	if count != len(tab.entries) || prev != tab.tail || sum != tab.Bytes || tab.Bytes < 0 || tab.Bytes > tab.MaxBytes || count > tab.MaxPackets {
		t.Fatalf("state count=%d sum=%d bytes=%d", count, sum, tab.Bytes)
	}
}
func FuzzReassemblyState(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < 2 {
			return
		}
		tab := New(metrics.New())
		tab.MaxPackets = int(b[0]%16) + 1
		tab.MaxBytes = 256 + int(b[1])*32
		for i := 2; i+5 < len(b) && i < 10000; i += 6 {
			id := uint64(b[i])<<8 | uint64(b[i+1])
			id++
			total := int(b[i+2]) + 1
			off := int(b[i+3]) % total
			n := int(b[i+4])%(total-off) + 1
			now := time.Unix(0, int64(i)*int64(time.Millisecond))
			tab.Add(proto.Frame{Type: 1, ID: id, Total: total, Offset: off, Payload: bytes.Repeat([]byte{b[i+5]}, n)}, now)
			assertState(t, tab)
		}
		tab.Expire(time.Unix(10000, 0))
		assertState(t, tab)
	})
}

func TestPressureAdmitsNewFragmentedPacket(t *testing.T) {
	for _, evict := range []bool{false, true} {
		tab := New(metrics.New())
		tab.EvictOldest = evict
		now := time.Now()
		for id := uint64(1); id <= 1024; id++ {
			tab.Add(pieces(id)[0], now)
		}
		var got []byte
		proto.Fragment(1025, bytes.Repeat([]byte{9}, 1500), 1100, func(b []byte) error {
			f, _ := proto.Decode(b)
			if p := tab.Add(f, now); p != nil {
				got = p
			}
			return nil
		})
		if (len(got) == 1500) != evict {
			t.Fatalf("evict=%v delivered=%d", evict, len(got))
		}
		assertState(t, tab)
	}
}

func TestOverlappingSessionGauges(t *testing.T) {
	m := metrics.New()
	a, b := New(m), New(m)
	now := time.Now()
	f := proto.Frame{Type: proto.Data, ID: 1, Total: 1500, Payload: make([]byte, 1100)}
	a.Add(f, now)
	b.Add(f, now)
	a.Release()
	// The remaining receiver must retain its accounting when the old one exits.
	if !strings.Contains(m.JSON(), `"reassembly_memory_bytes":1700`) || !strings.Contains(m.JSON(), `"reassembly_pending_packets":1`) {
		t.Fatal(m.JSON())
	}
	b.Release()
	if !strings.Contains(m.JSON(), `"reassembly_memory_bytes":0`) {
		t.Fatal(m.JSON())
	}
}

func TestJumboPacket(t *testing.T) {
	p := bytes.Repeat([]byte{0x73}, 9000)
	tab := New(metrics.New())
	var got []byte
	err := proto.Fragment(1, p, 1100, func(b []byte) error {
		f, e := proto.Decode(b)
		if e != nil {
			return e
		}
		if out := tab.Add(f, time.Now()); out != nil {
			got = out
		}
		return nil
	})
	if err != nil || !bytes.Equal(p, got) {
		t.Fatal("jumbo reassembly", err, len(got))
	}
	if tab.Bytes != 0 {
		t.Fatal("retained completed packet")
	}
}
