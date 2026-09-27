package tunnel

import (
	"context"
	"dtun/internal/metrics"
	"dtun/internal/proto"
	"dtun/internal/reassembly"
	"dtun/internal/settings"
	"dtun/internal/transport"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Run drains the TUN continuously. Disconnected/full queues drop whole packets.
func Run(ctx context.Context, tun io.ReadWriteCloser, c transport.Config, m *metrics.Metrics) error {
	limits, err := c.Limits.Resolve()
	if err != nil {
		return err
	}
	c.Limits = limits
	ctx, stop := context.WithCancel(ctx)
	closeOnCancel := context.AfterFunc(ctx, func() { _ = tun.Close() })
	var workers sync.WaitGroup
	defer func() { stop(); closeOnCancel(); _ = tun.Close(); workers.Wait() }()
	var active atomic.Bool
	q := make(chan []byte, limits.QueuePackets)
	tunErr := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		b := make([]byte, 65536)
		for {
			n, e := tun.Read(b)
			if e != nil {
				tunErr <- e
				return
			}
			m.Add("tun_rx_packets", 1)
			m.Add("tun_rx_bytes", int64(n))
			if !proto.ValidIP(b[:n]) || n > limits.MTU {
				m.Add("tun_invalid", 1)
				continue
			}
			if !active.Load() {
				m.Add("disconnected_drops", 1)
				if c.TraceICMPDrops {
					traceICMPDrop(b[:n], "disconnected")
				}
				continue
			}
			if !enqueuePacket(q, b[:n]) {
				m.Add("egress_queue_drops", 1)
				if c.TraceICMPDrops {
					traceICMPDrop(b[:n], "queue_full")
				}
			}
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Runtime()
				m.Set("egress_queue_depth", int64(len(q)))
				log.Printf("stats %s", m.JSON())
			}
		}
	}()
	return manageSessions(ctx, tun, q, c, m, &active, tunErr)
}

func runSession(parent context.Context, s transport.Session, tun io.Writer, q <-chan []byte, m *metrics.Metrics) error {
	return runSessionTX(parent, s, tun, q, m, nil, nil)
}
func runSessionTX(parent context.Context, s transport.Session, tun io.Writer, q <-chan []byte, m *metrics.Metrics, stopTX <-chan struct{}, txDone chan struct{}) error {
	return runSessionLimits(parent, s, tun, q, m, stopTX, txDone, settings.Defaults())
}
func runSessionLimits(parent context.Context, s transport.Session, tun io.Writer, q <-chan []byte, m *metrics.Metrics, stopTX <-chan struct{}, txDone chan struct{}, limits settings.Limits) error {

	var txOnce sync.Once
	markTXDone := func() {
		if txDone != nil {
			txOnce.Do(func() { close(txDone) })
		}
	}
	defer markTXDone()

	ctx, cancel := context.WithTimeout(parent, time.Hour)
	defer cancel()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	ids, e := proto.NewIDs()
	if e != nil {
		return e
	}
	var lastRX atomic.Int64
	lastRX.Store(time.Now().UnixNano())
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer markTXDone()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		lastSent := time.Now()
		send := func(b []byte) error {
			if e := s.WriteMessage(b); e != nil {
				m.Add("dtls_write_errors", 1)
				return e
			}
			lastSent = time.Now()
			return nil
		}
		for {
			select {
			case <-stopTX:
				return
			default:
			}
			select {
			case <-stopTX:
				return
			case <-ctx.Done():
				return
			case p := <-q:
				id, e := ids.Next()
				if e == nil {
					if len(p) > 1100 {
						m.Add("inner_packets_fragmented", 1)
					}
					e = proto.Fragment(id, p, 1100, func(b []byte) error {
						if e := send(b); e != nil {
							return e
						}
						m.Add("fragments_tx", 1)
						return nil
					})
				}
				if e != nil {
					errs <- e
					return
				}
			case <-tick.C:
				if time.Since(time.Unix(0, lastRX.Load())) > 75*time.Second {
					errs <- fmt.Errorf("peer idle timeout")
					return
				}
				if time.Since(lastSent) >= 20*time.Second {
					b, e := proto.Encode(proto.Frame{Type: proto.Ping})
					if e == nil {
						e = send(b)
					}
					if e != nil {
						errs <- e
						return
					}
					m.Add("keepalive_ping", 1)
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		table := reassembly.New(m)
		table.MaxPackets = limits.ReassemblyPackets
		table.MaxBytes = limits.ReassemblyBytes
		table.Timeout = limits.ReassemblyTimeout
		defer table.Release()
		b := make([]byte, 2048)
		// A separate ticker must expire fragment state even when no data arrives.
		incoming := make(chan []byte, 16)
		readErr := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				n, e := s.ReadMessage(b)
				if e != nil {
					readErr <- e
					return
				}
				p := append([]byte(nil), b[:n]...)
				select {
				case incoming <- p:
				case <-ctx.Done():
					return
				}
			}
		}()
		defer func() { _ = s.Close(); <-done }()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-readErr:
				m.Add("dtls_read_errors", 1)
				errs <- e
				return
			case now := <-tick.C:
				table.Expire(now)
			case b := <-incoming:
				f, e := proto.Decode(b)
				if e != nil {
					m.Add("reassembly_invalid", 1)
					continue
				}
				lastRX.Store(time.Now().UnixNano())
				switch f.Type {
				case proto.Ping:
					reply, encErr := proto.Encode(proto.Frame{Type: proto.Pong, ID: f.ID})
					if encErr != nil {
						errs <- encErr
						return
					}
					if e = s.WriteMessage(reply); e != nil {
						errs <- e
						return
					}
					m.Add("keepalive_pong", 1)
				case proto.Pong:
					m.Add("keepalive_pong_rx", 1)
				case proto.Data:
					if f.Total > limits.MTU {
						m.Add("inner_mtu_exceeded_rx", 1)
						continue
					}
					m.Add("fragments_rx", 1)
					p := table.Add(f, time.Now())
					if p != nil {
						if !proto.ValidIP(p) {
							m.Add("tun_invalid", 1)
							continue
						}
						n, e := tun.Write(p)
						if e != nil || n != len(p) {
							errs <- fmt.Errorf("TUN write %d/%d: %v", n, len(p), e)
							return
						}
						m.Add("tun_tx_packets", 1)
						m.Add("tun_tx_bytes", int64(n))
					}
				}
			}
		}
	}()
	select {
	case <-ctx.Done():
		e = ctx.Err()
	case e = <-errs:
	}
	cancel()
	_ = s.Close()
	wg.Wait()
	return e
}

// Opt-in diagnostics log only echo metadata, never payload or traffic keys.
func traceICMPDrop(p []byte, reason string) {
	if len(p) < 20 || p[0]>>4 != 4 || p[9] != 1 || binary.BigEndian.Uint16(p[6:8])&0x1fff != 0 {
		return
	}
	off := int(p[0]&15) * 4
	if off < 20 || len(p) < off+8 || (p[off] != 0 && p[off] != 8) {
		return
	}
	log.Printf("icmp_drop reason=%s src=%s dst=%s type=%d id=%d seq=%d", reason, net.IP(p[12:16]), net.IP(p[16:20]), p[off], binary.BigEndian.Uint16(p[off+4:off+6]), binary.BigEndian.Uint16(p[off+6:off+8]))
}

// Run has exactly one queue producer. Avoid allocating/copying packets that
// will immediately be dropped; consumers can only make more room.
func enqueuePacket(q chan []byte, p []byte) bool {
	if len(q) == cap(q) {
		return false
	}
	q <- append([]byte(nil), p...)
	return true
}
