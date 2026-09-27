// Package settings holds local resource limits; these do not change the wire format.
package settings

import (
	"dtun/internal/proto"
	"fmt"
	"time"
)

type Limits struct {
	MTU                                                                 int
	QueuePackets, ReassemblyPackets, ReassemblyBytes, SocketBufferBytes int
	ReassemblyTimeout, DrainTimeout, HandshakeTimeout                   time.Duration
}

func Defaults() Limits {
	return Limits{MTU: 1500, QueuePackets: 256, ReassemblyPackets: 1024, ReassemblyBytes: 4 << 20, SocketBufferBytes: 4 << 20, ReassemblyTimeout: time.Second, DrainTimeout: time.Second, HandshakeTimeout: 10 * time.Second}
}

// Resolve preserves zero-value Config compatibility for library callers.
// The CLI validates its already-defaulted values directly, rejecting explicit zero.
func (l Limits) Resolve() (Limits, error) {
	d := Defaults()
	if l.MTU == 0 {
		l.MTU = d.MTU
	}
	if l.QueuePackets == 0 {
		l.QueuePackets = d.QueuePackets
	}
	if l.ReassemblyPackets == 0 {
		l.ReassemblyPackets = d.ReassemblyPackets
	}
	if l.ReassemblyBytes == 0 {
		l.ReassemblyBytes = d.ReassemblyBytes
	}
	if l.SocketBufferBytes == 0 {
		l.SocketBufferBytes = d.SocketBufferBytes
	}
	if l.ReassemblyTimeout == 0 {
		l.ReassemblyTimeout = d.ReassemblyTimeout
	}
	if l.DrainTimeout == 0 {
		l.DrainTimeout = d.DrainTimeout
	}
	if l.HandshakeTimeout == 0 {
		l.HandshakeTimeout = d.HandshakeTimeout
	}
	return l, l.Validate()
}
func (l Limits) Validate() error {
	for _, x := range []struct {
		name            string
		value, min, max int
	}{
		{"mtu", l.MTU, 576, proto.MaxInner},
		{"queue-packets", l.QueuePackets, 1, 65536},
		{"reassembly-packets", l.ReassemblyPackets, 1, 4096},
		{"reassembly-bytes", l.ReassemblyBytes, 1700, 64 << 20},
		{"socket-buffer-bytes", l.SocketBufferBytes, 64 << 10, 64 << 20},
	} {
		if x.value < x.min || x.value > x.max {
			return fmt.Errorf("%s must be %d..%d", x.name, x.min, x.max)
		}
	}
	for _, x := range []struct {
		name            string
		value, min, max time.Duration
	}{
		{"reassembly-timeout", l.ReassemblyTimeout, 100 * time.Millisecond, 30 * time.Second},
		{"drain-timeout", l.DrainTimeout, 100 * time.Millisecond, 30 * time.Second},
		{"handshake-timeout", l.HandshakeTimeout, 500 * time.Millisecond, 60 * time.Second},
	} {
		if x.value < x.min || x.value > x.max {
			return fmt.Errorf("%s must be %s..%s", x.name, x.min, x.max)
		}
	}
	capacity := 2
	for capacity < (l.MTU+1099)/1100 {
		capacity *= 2
	}
	required := l.MTU + 192 + capacity*4
	if l.ReassemblyBytes < required {
		return fmt.Errorf("reassembly-bytes must be at least %d for mtu %d", required, l.MTU)
	}
	return nil
}
