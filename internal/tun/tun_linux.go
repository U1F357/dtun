//go:build linux

package tun

import (
	"dtun/internal/proto"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
)

func Open(name, address string) (*os.File, error) { return OpenWithMTU(name, address, 1500) }
func OpenWithMTU(name, address string, mtu int) (*os.File, error) {
	if mtu < 576 || mtu > proto.MaxInner {
		return nil, fmt.Errorf("TUN MTU must be 576..%d", proto.MaxInner)
	}
	fd, e := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	req, e := unix.NewIfreq(name)
	if e != nil {
		unix.Close(fd)
		return nil, e
	}
	req.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if e = unix.IoctlIfreq(fd, unix.TUNSETIFF, req); e != nil {
		unix.Close(fd)
		return nil, e
	}
	// Attach the device before registering its poll readiness with Go.
	f := os.NewFile(uintptr(fd), "/dev/net/tun")
	for _, args := range [][]string{{"address", "replace", address, "dev", name}, {"link", "set", "dev", name, "mtu", strconv.Itoa(mtu), "up"}} {
		if out, e := exec.Command("ip", args...).CombinedOutput(); e != nil {
			f.Close()
			return nil, fmt.Errorf("ip: %s: %w", out, e)
		}
	}
	return f, nil
}
