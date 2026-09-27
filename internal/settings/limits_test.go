package settings

import (
	"testing"
	"time"
)

func TestLimits(t *testing.T) {
	d := Defaults()
	if e := d.Validate(); e != nil {
		t.Fatal(e)
	}
	got, e := (Limits{}).Resolve()
	if e != nil || got != d {
		t.Fatal(got, e)
	}
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MTU = 9001 },
		func(l *Limits) { l.MTU = 9000; l.ReassemblyBytes = 1700 },
		func(l *Limits) { l.QueuePackets = 0 }, func(l *Limits) { l.QueuePackets = 65537 },
		func(l *Limits) { l.ReassemblyPackets = 4097 }, func(l *Limits) { l.ReassemblyBytes = 1699 },
		func(l *Limits) { l.DrainTimeout = 31 * time.Second }, func(l *Limits) { l.HandshakeTimeout = -1 },
		func(l *Limits) { l.ReassemblyTimeout = time.Millisecond }, func(l *Limits) { l.SocketBufferBytes = 1 },
	} {
		l := d
		change(&l)
		if l.Validate() == nil {
			t.Fatal("invalid limit accepted", l)
		}
	}
}

func TestMTUBudgets(t *testing.T) {
	for _, mtu := range []int{576, 1280, 1500, 9000} {
		l := Defaults()
		l.MTU = mtu
		if e := l.Validate(); e != nil {
			t.Fatal(mtu, e)
		}
	}
	l := Defaults()
	l.MTU = 575
	if l.Validate() == nil {
		t.Fatal("too-small MTU accepted")
	}
}
