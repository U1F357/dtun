package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"dtun/internal/metrics"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func identity(t *testing.T) Config {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if e != nil {
		t.Fatal(e)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	dir := t.TempDir()
	c := Config{Cert: filepath.Join(dir, "cert"), Key: filepath.Join(dir, "key")}
	os.WriteFile(c.Cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(c.Key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600)
	cert, _ := x509.ParseCertificate(der)
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	c.Pin = hex.EncodeToString(sum[:])
	return c
}
func pair(t *testing.T) (Config, Config) {
	a, b := identity(t), identity(t)
	a.Pin, b.Pin = b.Pin, a.Pin
	u, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	addr := u.LocalAddr().String()
	u.Close()
	a.Address = addr
	b.Address = addr
	b.Server = true
	b.AllowIP = "127.0.0.1"
	return a, b
}
func TestMutualDTLS(t *testing.T) {
	a, b := pair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := make(chan Session, 1)
	errs := make(chan error, 1)
	go func() {
		s, e := Open(ctx, b, metrics.New())
		if e != nil {
			errs <- e
			return
		}
		srv <- s
	}()
	time.Sleep(50 * time.Millisecond)
	c, e := Open(ctx, a, metrics.New())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var s Session
	select {
	case s = <-srv:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer s.Close()
	for _, n := range []int{16, 1116, 416} {
		p := bytes.Repeat([]byte{21}, n)
		if e := c.WriteMessage(p); e != nil {
			t.Fatal(e)
		}
		buf := make([]byte, 2048)
		nn, e := s.ReadMessage(buf)
		if e != nil || !bytes.Equal(p, buf[:nn]) {
			t.Fatal(nn, e)
		}
	}
}
func TestWrongPin(t *testing.T) {
	a, b := pair(t)
	a.Pin = hex.EncodeToString(make([]byte, 32))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s, _ := Open(ctx, b, metrics.New())
		if s != nil {
			s.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	s, e := Open(ctx, a, metrics.New())
	if s != nil {
		s.Close()
	}
	if e == nil {
		t.Fatal("wrong pin accepted")
	}
	cancel()
	<-done
}
func TestWireCap(t *testing.T) {
	u, _ := net.ListenUDP("udp4", nil)
	defer u.Close()
	w := &wireConn{UDPConn: u, m: metrics.New()}
	if _, e := w.WriteTo(make([]byte, 1201), u.LocalAddr()); e == nil {
		t.Fatal("oversize accepted")
	}
}

// The proxy duplicates an authenticated ciphertext and flips a ciphertext bit.
// Neither may produce an additional application datagram.
func TestCiphertextReplayAndTamper(t *testing.T) {
	a, b := pair(t)
	upstream, _ := net.ResolveUDPAddr("udp4", b.Address)
	front, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	back, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer front.Close()
	defer back.Close()
	a.Address = front.LocalAddr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var client *net.UDPAddr
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, e := front.ReadFromUDP(buf)
			if e != nil {
				return
			}
			mu.Lock()
			client = addr
			mu.Unlock()
			if n > 40 && buf[0] == 23 {
				bad := append([]byte(nil), buf[:n]...)
				bad[len(bad)-1] ^= 1
				back.WriteToUDP(bad, upstream) // tamper BEFORE valid sequence is seen
				back.WriteToUDP(buf[:n], upstream)
			}
			back.WriteToUDP(buf[:n], upstream) // replay if application data
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, e := back.ReadFromUDP(buf)
			if e != nil {
				return
			}
			mu.Lock()
			addr := client
			mu.Unlock()
			if addr != nil {
				front.WriteToUDP(buf[:n], addr)
			}
		}
	}()
	srv := make(chan Session, 1)
	errs := make(chan error, 1)
	go func() {
		s, e := Open(ctx, b, metrics.New())
		if e != nil {
			errs <- e
		} else {
			srv <- s
		}
	}()
	time.Sleep(50 * time.Millisecond)
	c, e := Open(ctx, a, metrics.New())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var s Session
	select {
	case s = <-srv:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer s.Close()
	p := bytes.Repeat([]byte{9}, 1116)
	if e := c.WriteMessage(p); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 2048)
	n, e := s.ReadMessage(buf)
	if e != nil || !bytes.Equal(buf[:n], p) {
		t.Fatal(n, e)
	}
	raw := s.(*secureSession).c
	raw.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, e := raw.Read(buf); e == nil {
		t.Fatalf("replay/tamper accepted: %d", n)
	}
}
func TestServerRejectsClientPin(t *testing.T) {
	a, b := pair(t)
	b.Pin = hex.EncodeToString(make([]byte, 32))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		s, e := Open(ctx, b, metrics.New())
		if s != nil {
			s.Close()
		}
		result <- e
	}()
	time.Sleep(50 * time.Millisecond)
	c, _ := Open(ctx, a, metrics.New())
	if c != nil {
		c.Close()
	}
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("untrusted client accepted")
		}
	case <-ctx.Done():
		t.Fatal("server did not reject")
	}
}
