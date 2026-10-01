//go:build !linux && !windows

package tun

import (
	"errors"
	"os"
)

func Open(name, address string) (*os.File, error) {
	return nil, errors.New("TUN requires Linux or Windows")
}

func OpenWithMTU(name, address string, mtu int) (*os.File, error) {
	return nil, errors.New("TUN requires Linux or Windows")
}
