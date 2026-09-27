package tunnel

import (
	"bytes"
	"context"
	"dtun/internal/metrics"
	"dtun/internal/proto"
	"dtun/internal/settings"
	"dtun/internal/transport"
	"encoding/binary"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func newFake() *fakeSession {
	return &fakeSession{make(chan []byte, 32), make(chan []byte, 32), make(chan struct{}), sync.Once{}}
}
func (s *fakeSession) ReadMessage(b []byte) (int, error) {
	select {
	case p := <-s.in:
		return copy(b, p), nil
	case <-s.done:
		return 0, errors.New("closed")
	}
}
func (s *fakeSession) WriteMessage(b []byte) error {
	select {
	case s.out <- append([]byte(nil), b...):
		return nil
	case <-s.done:
		return errors.New("closed")
	}
}
func (s *fakeSession) Close() error { s.once.Do(func() { close(s.done) }); return nil }

type packetSink struct{ packets chan []byte }

func (s packetSink) Write(b []byte) (int, error) {
	s.packets <- append([]byte(nil), b...)
	return len(b), nil
}
func testPacket() []byte {
	p := make([]byte, 1500)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], 1500)
	return p
}
func TestSessionLossMalformedAndNoRetransmit(t *testing.T) {
	s := newFake()
	sink := packetSink{make(chan []byte, 8)}
	m := metrics.New()
	q := make(chan []byte, 1)
	q <- testPacket()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSession(ctx, s, sink, q, m) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("session did not stop")
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case b := <-s.out:
			f, e := proto.Decode(b)
			if e != nil || f.Type != proto.Data {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("missing DATA")
		}
	}
	// No acknowledgements or retransmissions are sent even if only one fragment arrives.
	var frames [][]byte
	proto.Fragment(1, testPacket(), 1100, func(b []byte) error { frames = append(frames, b); return nil })
	s.in <- frames[0]
	s.in <- []byte{1, 2, 3}
	time.Sleep(1200 * time.Millisecond)
	select {
	case <-s.out:
		t.Fatal("unexpected retransmission/control response")
	default:
	}
	select {
	case <-sink.packets:
		t.Fatal("partial packet delivered")
	default:
	}
	var counters map[string]int64
	json.Unmarshal([]byte(m.JSON()), &counters)
	if counters["reassembly_timeout"] != 1 || counters["reassembly_memory_bytes"] != 0 {
		t.Fatal(counters)
	}
	s.in <- frames[1] // late fragment must not revive expired packet
	proto.Fragment(2, testPacket(), 1100, func(b []byte) error { s.in <- b; s.in <- b; return nil })
	select {
	case p := <-sink.packets:
		if !bytes.Equal(p, testPacket()) {
			t.Fatal("changed IP")
		}
	case <-time.After(time.Second):
		t.Fatal("valid packet missing")
	}
	time.Sleep(20 * time.Millisecond)
	select {
	case <-sink.packets:
		t.Fatal("duplicate delivery")
	default:
	}
}
func TestSessionChurn(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		s := newFake()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runSession(ctx, s, packetSink{make(chan []byte, 1)}, make(chan []byte), metrics.New()) }()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("teardown stuck")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := runtime.NumGoroutine(); got > before+3 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

type boundarySession struct {
	*fakeSession
	first            sync.Once
	entered, release chan struct{}
}

func (s *boundarySession) WriteMessage(b []byte) error {
	err := s.fakeSession.WriteMessage(b)
	s.first.Do(func() { close(s.entered); <-s.release })
	return err
}
func TestHandoverFinishesPacketAndDrainsReceiver(t *testing.T) {
	s := &boundarySession{fakeSession: newFake(), entered: make(chan struct{}), release: make(chan struct{})}
	sink := packetSink{make(chan []byte, 4)}
	q := make(chan []byte, 2)
	q <- testPacket()
	q <- testPacket()
	stopTX, txDone := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runSessionTX(ctx, s, sink, q, metrics.New(), stopTX, txDone) }()
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("sender not entered")
	}
	close(stopTX)
	close(s.release)
	select {
	case <-txDone:
	case <-time.After(time.Second):
		t.Fatal("sender did not stop")
	}
	if len(s.out) != 2 || len(q) != 1 {
		t.Fatalf("partial send or lost queue: frames=%d queued=%d", len(s.out), len(q))
	}
	// The old receiver still accepts both late fragments after its sender stops.
	proto.Fragment(5, testPacket(), 1100, func(b []byte) error { s.in <- b; return nil })
	select {
	case p := <-sink.packets:
		if !bytes.Equal(p, testPacket()) {
			t.Fatal("corrupt drain")
		}
	case <-time.After(time.Second):
		t.Fatal("old receiver closed prematurely")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain shutdown stuck")
	}
}

func TestFullQueueDoesNotAllocate(t *testing.T) {
	q := make(chan []byte, 1)
	q <- testPacket()
	p := testPacket()
	allocs := testing.AllocsPerRun(1000, func() {
		if enqueuePacket(q, p) {
			t.Fatal("accepted full queue")
		}
	})
	if allocs != 0 {
		t.Fatal("full queue allocated", allocs)
	}
	<-q
	if !enqueuePacket(q, p) {
		t.Fatal("rejected space")
	}
	got := <-q
	p[0] = 0
	if got[0] != 0x45 {
		t.Fatal("did not take ownership")
	}
}

type blockingTUN struct {
	done chan struct{}
	once sync.Once
}

func (b *blockingTUN) Read([]byte) (int, error)  { <-b.done; return 0, errors.New("closed") }
func (b *blockingTUN) Write([]byte) (int, error) { <-b.done; return 0, errors.New("closed") }
func (b *blockingTUN) Close() error              { b.once.Do(func() { close(b.done) }); return nil }
func TestRunCancellationOwnsTUN(t *testing.T) {
	tun := &blockingTUN{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, tun, transport.Config{Address: "127.0.0.1:12345"}, metrics.New()) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run leaked blocked TUN reader")
	}
	select {
	case <-tun.done:
	default:
		t.Fatal("TUN not closed")
	}
}
func TestConfiguredReassemblyTimeout(t *testing.T) {
	s := newFake()
	sink := packetSink{make(chan []byte, 4)}
	m := metrics.New()
	l := settings.Defaults()
	l.ReassemblyTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSessionLimits(ctx, s, sink, make(chan []byte), m, nil, nil, l) }()
	defer func() { cancel(); <-done }()
	proto.Fragment(1, testPacket(), 1100, func(b []byte) error {
		f, _ := proto.Decode(b)
		if f.Offset == 0 {
			s.in <- b
		}
		return nil
	})
	time.Sleep(350 * time.Millisecond)
	var counters map[string]int64
	json.Unmarshal([]byte(m.JSON()), &counters)
	if counters["reassembly_timeout"] != 1 {
		t.Fatal(counters)
	}
}
func BenchmarkFullQueue(b *testing.B) {
	q := make(chan []byte, 256)
	p := testPacket()
	for i := 0; i < cap(q); i++ {
		q <- p
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enqueuePacket(q, p)
	}
}
