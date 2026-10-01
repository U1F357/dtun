//go:build windows

package tun

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"
)

func TestWindowsInvalidConfig(t *testing.T) {
	for _, tc := range []struct {
		addr string
		mtu  int
	}{{"bad", 1500}, {"192.0.2.1/30", 575}, {"2001:db8::1/64", 1000}} {
		if d, err := OpenWithMTU("unused", tc.addr, tc.mtu); err == nil {
			d.Close()
			t.Fatal("accepted invalid config", tc)
		}
	}
}

// Opt-in: creates only a temporary adapter and a connected benchmark subnet.
func TestWintunPacketsAndClose(t *testing.T) {
	if os.Getenv("DTUN_TEST_WINTUN") != "1" {
		t.Skip("requires Windows administrator and adjacent wintun.dll")
	}
	d, err := Open("dtun-ci", "198.18.0.1/30")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if other, err := Open("dtun-ci", "198.18.0.1/30"); err == nil {
		other.Close()
		t.Fatal("reused an existing adapter")
	}
	conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.IPv4(198, 18, 0, 1)}, &net.UDPAddr{IP: net.IPv4(198, 18, 0, 2), Port: 23456})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := []byte("dtun-wintun-roundtrip")
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := d.Read(buf)
			if err != nil {
				done <- err
				return
			}
			p := buf[:n]
			if n < 28 || p[0] != 0x45 || p[9] != 17 || !bytes.Equal(p[28:], payload) {
				continue
			}
			// Turn the captured outbound IPv4/UDP packet into an inbound response.
			reply := append([]byte(nil), p...)
			copy(reply[12:16], p[16:20])
			copy(reply[16:20], p[12:16])
			copy(reply[20:22], p[22:24])
			copy(reply[22:24], p[20:22])
			reply[26], reply[27] = 0, 0 // IPv4 UDP permits no checksum.
			reply[10], reply[11] = 0, 0
			var sum uint32
			for i := 0; i < 20; i += 2 {
				sum += uint32(binary.BigEndian.Uint16(reply[i : i+2]))
			}
			for sum>>16 != 0 {
				sum = (sum & 65535) + (sum >> 16)
			}
			binary.BigEndian.PutUint16(reply[10:12], ^uint16(sum))
			_, err = d.Write(reply)
			done <- err
			return
		}
	}()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 100)
	n, err := conn.Read(reply)
	if err != nil || !bytes.Equal(reply[:n], payload) {
		t.Fatalf("UDP roundtrip: %q %v", reply[:n], err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		buf := make([]byte, 65536)
		for {
			if _, err := d.Read(buf); err != nil {
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	d.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Read")
	}
	d.Close()
	if _, err := d.Write(payload); err == nil {
		t.Fatal("Write after Close succeeded")
	}
	if _, err := d.Read(reply); err == nil {
		t.Fatal("Read after Close succeeded")
	}
}
