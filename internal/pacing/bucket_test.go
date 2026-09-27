package pacing

import (
	"context"
	"testing"
	"time"
)

func TestRateAndIdleBurst(t *testing.T) {
	b, _ := New(80_000_000)
	now := time.Now()
	b.last = now
	b.tokens = 0
	if wait := b.reserve(1000, now); wait != 100*time.Microsecond {
		t.Fatal(wait)
	}
	if wait := b.reserve(1000, now.Add(100*time.Microsecond)); wait != 0 {
		t.Fatal(wait)
	}
	if wait := b.reserve(1000, now.Add(100*time.Microsecond)); wait != 100*time.Microsecond {
		t.Fatal(wait)
	}
	if wait := b.reserve(BurstBytes, now.Add(time.Hour)); wait != 0 {
		t.Fatal(wait)
	}
	if b.tokens != 0 {
		t.Fatal("idle accumulated more than burst", b.tokens)
	}
}
func TestCancellationAndValidation(t *testing.T) {
	if _, e := New(-1); e == nil {
		t.Fatal("negative rate")
	}
	b, _ := New(1)
	b.tokens = 0
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	if e := b.Wait(ctx, 1200); e == nil {
		t.Fatal("expected cancel")
	}
	if time.Since(start) > time.Second {
		t.Fatal("slow cancel")
	}
	if e := b.Wait(context.Background(), BurstBytes+1); e == nil {
		t.Fatal("oversized reservation")
	}
}
func TestSustainedCap(t *testing.T) {
	b, _ := New(80_000_000)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	for i := 0; i < 1000; i++ {
		if e := b.Wait(ctx, 1200); e != nil {
			t.Fatal(e)
		}
	}
	minimum := time.Duration(float64(1200000-BurstBytes) / 10_000_000 * float64(time.Second))
	if time.Since(start) < minimum-time.Millisecond {
		t.Fatal("rate exceeded")
	}
}
func TestDisabled(t *testing.T) {
	b, _ := New(0)
	for i := 0; i < 10000; i++ {
		if e := b.Wait(context.Background(), 1200); e != nil {
			t.Fatal(e)
		}
	}
}
