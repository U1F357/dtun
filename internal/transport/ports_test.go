package transport

import (
	"context"
	"dtun/internal/metrics"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestEndpoints(t *testing.T) {
	for _, ports := range []string{"0", "65536", "80,80", "80,", "x"} {
		if _, e := Endpoints(Config{Address: "127.0.0.1:80", Ports: ports}); e == nil {
			t.Fatal(ports)
		}
	}
	c := Config{Address: "127.0.0.1:80", Ports: "80,443,23333", SwitchInterval: time.Second}
	a, e := Endpoints(c)
	if e != nil || len(a) != 3 || a[2] != "127.0.0.1:23333" {
		t.Fatal(a, e)
	}
	c.Server = true
	if _, e = Endpoints(c); e == nil {
		t.Fatal("server rotation accepted")
	}
}
func TestMultiPortReuseAndShutdown(t *testing.T) {
	a, b := pair(t)
	sockets := make([]*net.UDPConn, 3)
	ports := ""
	for i := range sockets {
		u, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		sockets[i] = u
		if i > 0 {
			ports += ","
		}
		ports += fmt.Sprint(u.LocalAddr().(*net.UDPAddr).Port)
	}
	for _, u := range sockets {
		u.Close()
	}
	b.Ports = ports
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	accepted := AcceptPorts(ctx, b, metrics.New())
	addresses, _ := Endpoints(b)
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 9; i++ {
		a.Address = addresses[i%3]
		client, e := Open(ctx, a, metrics.New())
		if e != nil {
			t.Fatal(e)
		}
		var server Session
		select {
		case got := <-accepted:
			if got.Err != nil {
				t.Fatal(got.Err)
			}
			server = got.Session
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if e = client.WriteMessage([]byte("hello")); e != nil {
			t.Fatal(e)
		}
		buf := make([]byte, 100)
		n, e := server.ReadMessage(buf)
		if e != nil || string(buf[:n]) != "hello" {
			t.Fatal(n, e)
		}
		client.Close()
		server.Close()
	}
	cancel()
	select {
	case _, ok := <-accepted:
		if ok {
			t.Fatal("unexpected pending accept")
		}
	case <-time.After(time.Second):
		t.Fatal("listener workers leaked")
	}
}
