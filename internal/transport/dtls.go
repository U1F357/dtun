package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"dtun/internal/metrics"
	"dtun/internal/pacing"
	"dtun/internal/settings"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/pion/dtls/v3"
	"net"
	"sync"
	"time"
)

// Session preserves one application message per WriteMessage/ReadMessage.
type Session interface {
	ReadMessage([]byte) (int, error)
	WriteMessage([]byte) error
	Close() error
}
type secureSession struct {
	c      *dtls.Conn
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *secureSession) ReadMessage(b []byte) (int, error) {
	_ = s.c.SetReadDeadline(time.Now().Add(75 * time.Second))
	return s.c.Read(b)
}
func (s *secureSession) WriteMessage(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, e := s.c.Write(b)
	if e == nil && n != len(b) {
		return errors.New("short datagram write")
	}
	return e
}
func (s *secureSession) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return s.c.Close()
}

type Config struct {
	Cert, Key, Pin, Address, AllowIP string
	Server                           bool
	MaxRateBPS                       int64
	Ports                            string
	SwitchInterval                   time.Duration
	SharedPacer                      *pacing.Bucket
	TraceICMPDrops                   bool
	Limits                           settings.Limits
}

func CertificateConfig(c Config) (*dtls.Config, error) {
	cert, e := tls.LoadX509KeyPair(c.Cert, c.Key)
	if e != nil {
		return nil, e
	}
	pin, e := hex.DecodeString(c.Pin)
	if e != nil || len(pin) != 32 {
		return nil, errors.New("peer pin must be SHA256 SPKI hex")
	}
	verify := func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 {
			return errors.New("expected one pinned peer certificate")
		}
		peer, e := x509.ParseCertificate(raw[0])
		if e != nil {
			return e
		}
		sum := sha256.Sum256(peer.RawSubjectPublicKeyInfo)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return errors.New("peer SPKI pin mismatch")
		}
		now := time.Now()
		if now.Before(peer.NotBefore) || now.After(peer.NotAfter) {
			return errors.New("peer certificate expired or not yet valid")
		}
		return nil
	}
	return &dtls.Config{Certificates: []tls.Certificate{cert}, CipherSuites: []dtls.CipherSuiteID{dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}, ClientAuth: dtls.RequireAnyClientCert, InsecureSkipVerify: true, VerifyPeerCertificate: verify, ExtendedMasterSecret: dtls.RequireExtendedMasterSecret, MTU: 1100, ReplayProtectionWindow: 1024}, nil
}

// wireConn guards ACTUAL UDP payload length, including encrypted records and handshake.
// Only one endpoint may read the socket; server initial packet is retained for DTLS.
type wireConn struct {
	*net.UDPConn
	peer  *net.UDPAddr
	first []byte
	m     *metrics.Metrics
	pacer *pacing.Bucket
	ctx   context.Context
}

func (w *wireConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if w.first != nil {
		n := copy(b, w.first)
		w.first = nil
		return n, w.peer, nil
	}
	for {
		n, a, e := w.UDPConn.ReadFromUDP(b)
		if e != nil {
			return n, a, e
		}
		w.m.Add("outer_udp_packets_rx", 1)
		w.m.Add("outer_udp_bytes_rx", int64(n))
		w.m.Max("outer_udp_max_datagram_seen", int64(n))
		if !a.IP.Equal(w.peer.IP) || a.Port != w.peer.Port {
			w.m.Add("outer_peer_drops", 1)
			continue
		}
		if n > 1200 {
			w.m.Add("outer_oversize_rx", 1)
			continue
		}
		return n, a, nil
	}
}
func (w *wireConn) WriteTo(b []byte, a net.Addr) (int, error) {
	if len(b) > 1200 {
		w.m.Add("outer_oversize_tx_rejected", 1)
		return 0, fmt.Errorf("outer UDP payload %d exceeds 1200", len(b))
	}
	if w.pacer != nil {
		// IPv4 underlay only: account actual ciphertext + UDP(8) + IP(20).
		if e := w.pacer.Wait(w.ctx, len(b)+28); e != nil {
			return 0, e
		}
	}
	n, e := w.UDPConn.WriteTo(b, a)
	if e == nil {
		w.m.Add("outer_udp_packets_tx", 1)
		w.m.Add("outer_udp_bytes_tx", int64(n))
		w.m.Max("outer_udp_max_datagram_tx", int64(n))
	}
	return n, e
}
func Open(ctx context.Context, c Config, m *metrics.Metrics) (Session, error) {
	limits, err := c.Limits.Resolve()
	if err != nil {
		return nil, err
	}
	pacer := c.SharedPacer
	var e error
	if pacer == nil {
		pacer, e = pacing.New(c.MaxRateBPS)
		if e != nil {
			return nil, e
		}
	}
	cfg, e := CertificateConfig(c)
	if e != nil {
		return nil, e
	}
	addr, e := net.ResolveUDPAddr("udp4", c.Address)
	if e != nil {
		return nil, e
	}
	local := &net.UDPAddr{IP: net.IPv4zero}
	if c.Server {
		local = addr
	}
	u, e := net.ListenUDP("udp4", local)
	if e != nil {
		return nil, e
	}
	success := false
	paceCtx, paceCancel := context.WithCancel(ctx)
	defer func() {
		if !success {
			paceCancel()
			_ = u.Close()
		}
	}()
	if e = setDF(u); e != nil {
		return nil, e
	}
	if e = u.SetReadBuffer(limits.SocketBufferBytes); e != nil {
		return nil, e
	}
	if e = u.SetWriteBuffer(limits.SocketBufferBytes); e != nil {
		return nil, e
	}
	stop := context.AfterFunc(ctx, func() { _ = u.Close() })
	defer stop()
	w := &wireConn{UDPConn: u, peer: addr, m: m, pacer: pacer, ctx: paceCtx}
	m.Set("pacer_rate_bps", c.MaxRateBPS)
	m.Set("pacer_burst_bytes", pacing.BurstBytes)
	if c.Server {
		allowed := net.ParseIP(c.AllowIP)
		if allowed == nil {
			return nil, errors.New("server requires allow-ip")
		}
		b := make([]byte, 65535)
		for {
			n, a, e := u.ReadFromUDP(b)
			if e != nil {
				return nil, e
			}
			m.Add("outer_udp_packets_rx", 1)
			m.Add("outer_udp_bytes_rx", int64(n))
			if !a.IP.Equal(allowed) || n < 13 || n > 1200 || b[0] != 22 {
				m.Add("outer_peer_drops", 1)
				continue
			}
			w.peer = a
			w.first = append([]byte(nil), b[:n]...)
			break
		}
	}
	var d *dtls.Conn
	if c.Server {
		d, e = dtls.Server(w, w.peer, cfg)
	} else {
		d, e = dtls.Client(w, w.peer, cfg)
	}
	if e != nil {
		return nil, e
	}
	hctx, cancel := context.WithTimeout(ctx, limits.HandshakeTimeout)
	defer cancel()
	stopHandshakePacing := context.AfterFunc(hctx, paceCancel)
	defer stopHandshakePacing()
	m.Add("dtls_handshakes_total", 1)
	if e = d.HandshakeContext(hctx); e != nil {
		m.Add("dtls_handshake_failures", 1)
		paceCancel()
		_ = d.Close()
		return nil, e
	}
	success = true
	return &secureSession{c: d, cancel: paceCancel}, nil
}
