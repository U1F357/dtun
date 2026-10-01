//go:build !linux && !windows

package transport

import "net"

func setDF(c *net.UDPConn) error { return nil }
