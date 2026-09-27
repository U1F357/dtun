package tunnel

import (
	"context"
	"dtun/internal/metrics"
	"dtun/internal/pacing"
	"dtun/internal/settings"
	"dtun/internal/transport"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// A retired sender finishes its current entire IP packet. Its independent
// receiver then drains late ciphertext for one second with its own reassembly.
type liveSession struct {
	session              transport.Session
	stopTX, txDone, done chan struct{}
	cancel               context.CancelFunc
	err                  error
}

func startSession(ctx context.Context, s transport.Session, tun io.Writer, q <-chan []byte, m *metrics.Metrics, limits settings.Limits) *liveSession {
	ctx, cancel := context.WithCancel(ctx)
	l := &liveSession{session: s, stopTX: make(chan struct{}), txDone: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	go func() { l.err = runSessionLimits(ctx, s, tun, q, m, l.stopTX, l.txDone, limits); close(l.done) }()
	return l
}
func manageSessions(ctx context.Context, tun io.Writer, q chan []byte, c transport.Config, m *metrics.Metrics, active *atomic.Bool, tunErr <-chan error) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var current *liveSession
	currentAddress := ""
	defer func() {
		cancel()
		if current != nil {
			current.cancel()
			<-current.done
			current.session.Close()
		}
		wg.Wait()
		active.Store(false)
	}()
	retire := func(l *liveSession) {
		if l == nil {
			return
		}
		close(l.stopTX)
		<-l.txDone // stop at a whole-packet boundary, not between fragments
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-time.After(c.Limits.DrainTimeout):
			case <-ctx.Done():
			}
			l.cancel()
			<-l.done
			l.session.Close()
			m.Add("handover_drained_sessions", 1)
		}()
	}
	install := func(s transport.Session, address string) {
		if current == nil {
			// Only discard stale packets after a real disconnect, never during handover.
			for len(q) > 0 {
				<-q
			}
		} else {
			retire(current)
			m.Add("graceful_handovers", 1)
		}
		current = startSession(ctx, s, tun, q, m, c.Limits)
		currentAddress = address
		active.Store(true)
		log.Printf("session established endpoint=%s (DTLS 1.2 ECDHE-ECDSA AES128-GCM, SPKI pinned)", address)
	}
	addresses, err := transport.Endpoints(c)
	if err != nil {
		return err
	}
	c.SharedPacer, err = pacing.New(c.MaxRateBPS)
	if err != nil {
		return err
	}
	var accepted <-chan transport.Accepted
	if c.Server {
		accepted = transport.AcceptPorts(ctx, c, m)
	}
	type dialResult struct {
		session transport.Session
		address string
		err     error
	}
	dialed := make(chan dialResult)
	dialing := false
	index := 0
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var due <-chan time.Time
	schedule := func(d time.Duration) { timer.Reset(d); due = timer.C }
	if !c.Server {
		schedule(0)
	}
	for {
		var done <-chan struct{}
		if current != nil {
			done = current.done
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-tunErr:
			return err
		case <-done:
			log.Printf("session ended: %v", current.err)
			current.session.Close()
			current.cancel()
			current = nil
			active.Store(false)
			m.Add("dtls_session_resets", 1)
			if !c.Server && !dialing {
				schedule(0)
			}
		case a, ok := <-accepted:
			if !ok {
				return ctx.Err()
			}
			if a.Err != nil {
				log.Printf("listen endpoint=%s: %v", a.Address, a.Err)
				continue
			}
			install(a.Session, a.Address)
		case <-due:
			due = nil
			dialing = true
			if current != nil && len(addresses) > 1 && addresses[index] == currentAddress {
				index = (index + 1) % len(addresses)
			}
			cfg := c
			cfg.Address = addresses[index]
			index = (index + 1) % len(addresses)
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, e := transport.Open(ctx, cfg, m)
				select {
				case dialed <- dialResult{s, cfg.Address, e}:
				case <-ctx.Done():
					if s != nil {
						s.Close()
					}
				}
			}()
		case result := <-dialed:
			dialing = false
			if result.err != nil {
				log.Printf("session setup endpoint=%s: %v (keeping current session if healthy)", result.address, result.err)
				m.Add("candidate_handshake_failures", 1)
				schedule(2 * time.Second)
				continue
			}
			if current != nil {
				m.Add("scheduled_port_switches", 1)
			}
			install(result.session, result.address)
			if c.SwitchInterval > 0 {
				schedule(c.SwitchInterval)
			}
		}
	}
}
