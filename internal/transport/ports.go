package transport

import (
	"context"
	"dtun/internal/metrics"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

func Endpoints(c Config) ([]string, error) {
	if c.Server && net.ParseIP(c.AllowIP).To4() == nil {
		return nil, fmt.Errorf("server allow-ip must be a literal IPv4 address")
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return nil, err
	}
	ports := []string{port}
	if c.Ports != "" {
		ports = strings.Split(c.Ports, ",")
	}
	if len(ports) > 16 {
		return nil, fmt.Errorf("at most 16 ports")
	}
	seen := map[int]bool{}
	var result []string
	for _, p := range ports {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > 65535 || seen[n] {
			return nil, fmt.Errorf("invalid or duplicate port %q", p)
		}
		seen[n] = true
		result = append(result, net.JoinHostPort(host, strconv.Itoa(n)))
	}
	if c.SwitchInterval < 0 || (c.SwitchInterval > 0 && (c.Server || len(result) < 2 || c.SwitchInterval < time.Second)) {
		return nil, fmt.Errorf("switch-interval requires client, multiple ports, and at least 1s")
	}
	limits, e := c.Limits.Resolve()
	if e != nil {
		return nil, e
	}
	if c.SwitchInterval > 0 && limits.DrainTimeout > c.SwitchInterval {
		return nil, fmt.Errorf("drain-timeout must not exceed switch-interval")
	}
	for _, endpoint := range result {
		if _, err := net.ResolveUDPAddr("udp4", endpoint); err != nil {
			return nil, err
		}
	}
	return result, nil
}

type Accepted struct {
	Session Session
	Address string
	Err     error
}
type notifiedSession struct {
	Session
	once sync.Once
	done chan struct{}
}

func (s *notifiedSession) Close() error {
	var err error
	s.once.Do(func() { err = s.Session.Close(); close(s.done) })
	return err
}

// Each port has one bounded worker. Only authenticated sessions reach the
// tunnel, which drains the previous receiver after a packet-boundary handover.
func AcceptPorts(ctx context.Context, c Config, m *metrics.Metrics) <-chan Accepted {
	out := make(chan Accepted)
	addresses, err := Endpoints(c)
	if err != nil {
		close(out)
		return out
	}
	var wg sync.WaitGroup
	for _, address := range addresses {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			cfg := c
			cfg.Address = address
			for ctx.Err() == nil {
				session, err := Open(ctx, cfg, m)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case out <- Accepted{Address: address, Err: err}:
					case <-ctx.Done():
						return
					}
					select {
					case <-time.After(time.Second):
					case <-ctx.Done():
						return
					}
					continue
				}
				s := &notifiedSession{Session: session, done: make(chan struct{})}
				select {
				case out <- Accepted{Session: s, Address: address}:
				case <-ctx.Done():
					s.Close()
					return
				}
				select {
				case <-s.done:
				case <-ctx.Done():
					s.Close()
					return
				}
			}
		}(address)
	}
	go func() { wg.Wait(); close(out) }()
	return out
}
