//go:build linux

package transport

import (
	"golang.org/x/sys/unix"
	"net"
)

func setDF(c *net.UDPConn) error {
	r, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var se error
	e = r.Control(func(fd uintptr) {
		se = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
	})
	if e != nil {
		return e
	}
	return se
}
