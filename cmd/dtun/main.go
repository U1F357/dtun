package main

import (
	"context"
	"dtun/internal/metrics"
	"dtun/internal/settings"
	"dtun/internal/transport"
	"dtun/internal/tun"
	"dtun/internal/tunnel"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c := transport.Config{Limits: settings.Defaults()}
	var name, addr string
	flag.BoolVar(&c.Server, "server", false, "server mode")
	flag.StringVar(&c.Address, "endpoint", "", "server listen address or client remote, IPv4 UDP")
	flag.StringVar(&c.AllowIP, "allow-ip", "", "server accepts only this underlay IPv4")
	flag.StringVar(&c.Cert, "cert", "", "local certificate PEM")
	flag.StringVar(&c.Key, "key", "", "local private key PEM")
	flag.StringVar(&c.Pin, "peer-pin", "", "peer SHA256 SPKI hex")
	flag.StringVar(&name, "tun", "dtun0", "TUN name")
	flag.StringVar(&addr, "address", "10.255.255.1/30", "TUN address")
	flag.Int64Var(&c.MaxRateBPS, "max-rate-bps", 0, "fixed outer IPv4 bit rate; 0 disables pacing (no congestion feedback)")
	flag.StringVar(&c.Ports, "ports", "", "comma-separated UDP ports on endpoint host (max 16); overrides endpoint port")
	flag.DurationVar(&c.SwitchInterval, "switch-interval", 0, "client periodic port rotation, e.g. 10m; 0 disables")
	flag.BoolVar(&c.TraceICMPDrops, "trace-icmp-drops", false, "diagnostic: log ICMP echo identifiers on local queue/disconnect drops; no payload")
	flag.IntVar(&c.Limits.QueuePackets, "queue-packets", c.Limits.QueuePackets, "whole-IP-packet FIFO capacity, 1..65536")
	flag.IntVar(&c.Limits.ReassemblyPackets, "reassembly-packets", c.Limits.ReassemblyPackets, "pending packets per receive session, 1..4096")
	flag.IntVar(&c.Limits.ReassemblyBytes, "reassembly-bytes", c.Limits.ReassemblyBytes, "accounted reassembly bytes per session, 1700..67108864")
	flag.IntVar(&c.Limits.SocketBufferBytes, "socket-buffer-bytes", c.Limits.SocketBufferBytes, "requested UDP send/receive buffer bytes; OS may clamp")
	flag.DurationVar(&c.Limits.ReassemblyTimeout, "reassembly-timeout", c.Limits.ReassemblyTimeout, "partial packet lifetime, 100ms..30s")
	flag.DurationVar(&c.Limits.DrainTimeout, "drain-timeout", c.Limits.DrainTimeout, "old receiver grace after handover, 100ms..30s")
	flag.DurationVar(&c.Limits.HandshakeTimeout, "handshake-timeout", c.Limits.HandshakeTimeout, "candidate handshake timeout, 500ms..1m")
	flag.IntVar(&c.Limits.MTU, "mtu", c.Limits.MTU, "TUN MTU, 576..9000; configure both peers equally; IPv6 requires >=1280")
	flag.Parse()
	if e := c.Limits.Validate(); e != nil {
		log.Fatal(e)
	}
	log.Printf("local resource limits: %+v", c.Limits)
	if _, e := transport.Endpoints(c); e != nil {
		log.Fatal(e)
	}
	if c.MaxRateBPS < 0 {
		log.Fatal("max-rate-bps must be nonnegative")
	}
	log.Printf("fixed outer rate: %d bps (0=off), burst 16384 bytes", c.MaxRateBPS)
	if c.Address == "" {
		log.Fatal("endpoint required")
	}
	if c.Server && c.AllowIP == "" {
		log.Fatal("allow-ip required")
	}
	if _, e := transport.CertificateConfig(c); e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	f, e := tun.OpenWithMTU(name, addr, c.Limits.MTU)
	if e != nil {
		log.Fatal(e)
	}
	defer f.Close()
	go func() { <-ctx.Done(); f.Close() }()
	m := metrics.New()
	e = tunnel.Run(ctx, f, c, m)
	log.Printf("final stats %s", m.JSON())
	if e != nil && ctx.Err() == nil {
		log.Print(e)
		os.Exit(1)
	}
}
