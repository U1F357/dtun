//go:build linux

package tun

import (
	"golang.org/x/sys/unix"
	"os"
	"testing"
	"time"
)

// Run only in a disposable network namespace with CAP_NET_ADMIN.
func TestCloseUnblocksRead(t *testing.T) {
	if os.Getenv("DTUN_TEST_TUN") != "1" {
		t.Skip("requires isolated Linux netns and DTUN_TEST_TUN=1")
	}
	f, e := Open("dtcheck0", "192.0.2.1/30")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	raw, e := f.SyscallConn()
	if e != nil {
		t.Fatal(e)
	}
	var flags int
	var ferr error
	if e = raw.Control(func(fd uintptr) { flags, ferr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); e != nil || ferr != nil {
		t.Fatal(e, ferr)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("TUN descriptor is blocking")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 2048)
		for {
			if _, e := f.Read(b); e != nil {
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	f.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt TUN Read")
	}
}
