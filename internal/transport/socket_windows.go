//go:build windows

package transport

import (
	"golang.org/x/sys/windows"
	"net"
)

func setDF(c *net.UDPConn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		// Winsock IP_DONTFRAGMENT applies to the IPv4 UDP underlay.
		socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, 14 /* IP_DONTFRAGMENT (ws2ipdef.h) */, 1)
	})
	if err != nil {
		return err
	}
	return socketErr
}
