// Package pacing implements an optional fixed-rate token bucket. There is no
// congestion feedback, retransmission, or queue inside the bucket.
package pacing

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const BurstBytes = 16 * 1024

type Bucket struct {
	mu             sync.Mutex
	bytesPerSecond float64
	burst, tokens  float64
	last           time.Time
}

func New(bitsPerSecond int64) (*Bucket, error) {
	if bitsPerSecond < 0 {
		return nil, fmt.Errorf("max-rate-bps must be nonnegative")
	}
	return &Bucket{bytesPerSecond: float64(bitsPerSecond) / 8, burst: BurstBytes, tokens: BurstBytes, last: time.Now()}, nil
}

// reserve is called with mu held. Tokens are consumed only when a packet can
// actually proceed; a cancelled wait never leaves future reservations behind.
func (b *Bucket) reserve(n int, now time.Time) time.Duration {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed*b.bytesPerSecond)
		b.last = now
	}
	if b.tokens >= float64(n) {
		b.tokens -= float64(n)
		return 0
	}
	ns := math.Ceil((float64(n) - b.tokens) / b.bytesPerSecond * 1e9)
	// Bounded timer intervals also allow very low rates without duration overflow.
	if ns > float64(time.Second) {
		return time.Second
	}
	return time.Duration(ns)
}

// Wait serializes callers. n includes outer IP + UDP headers and encrypted
// payload. Its only input affecting the rate is the configured constant.
func (b *Bucket) Wait(ctx context.Context, n int) error {
	if n < 0 || n > BurstBytes {
		return fmt.Errorf("pacer packet size out of bounds: %d", n)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.bytesPerSecond == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := b.reserve(n, time.Now())
		if wait == 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
