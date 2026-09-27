package proto

import (
	"bytes"
	"math"
	"testing"
)

func TestCodec(t *testing.T) {
	for _, n := range []int{1, 100, 1100, 1101, 1499, 1500, 576, 1280, 4096, 9000} {
		p := bytes.Repeat([]byte{73}, n)
		var got []byte
		err := Fragment(1, p, 1100, func(b []byte) error {
			f, e := Decode(b)
			if e != nil {
				return e
			}
			if f.Offset != len(got) || f.Total != n {
				t.Fatal(f)
			}
			got = append(got, f.Payload...)
			return nil
		})
		if err != nil || !bytes.Equal(got, p) {
			t.Fatal(n, err)
		}
	}
}
func TestInvalid(t *testing.T) {
	base, _ := Encode(Frame{Data, 1, 10, 0, []byte{1}})
	for _, mut := range []func([]byte){func(b []byte) { b[0] = 3 }, func(b []byte) { b[1] = 9 }, func(b []byte) { b[2] = 21 }, func(b []byte) { b[3] = 1 }, func(b []byte) { b[13] = 0 }, func(b []byte) { b[12] = 255 }, func(b []byte) { b[15] = 10 }, func(b []byte) { b[14] = 255 }, func(b []byte) { b[11] = 0 }} {
		b := append([]byte(nil), base...)
		mut(b)
		if _, e := Decode(b); e == nil {
			t.Fatal(b)
		}
	}
	for _, b := range [][]byte{nil, base[:15], base[:16]} {
		if _, e := Decode(b); e == nil {
			t.Fatal(b)
		}
	}
}
func TestIDs(t *testing.T) {
	a, e := NewIDs()
	if e != nil {
		t.Fatal(e)
	}
	b, _ := NewIDs()
	if a.Value == b.Value {
		t.Fatal("session reset")
	}
	x, _ := a.Next()
	y, _ := a.Next()
	if y != x+1 {
		t.Fatal(x, y)
	}
	a.Value = math.MaxUint64
	if _, e = a.Next(); e == nil {
		t.Fatal("wrap")
	}
}
func FuzzDecode(f *testing.F) {
	b, _ := Encode(Frame{Data, 1, 3, 0, []byte{1, 2, 3}})
	f.Add(b)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		v, e := Decode(b)
		if e == nil {
			out, e := Encode(v)
			decoded, de := Decode(out)
			if e != nil || de != nil || decoded.Type != v.Type || decoded.ID != v.ID || decoded.Total != v.Total || decoded.Offset != v.Offset || !bytes.Equal(decoded.Payload, v.Payload) {
				t.Fatal("roundtrip")
			}
		}
	})
}

func TestRandomPadding(t *testing.T) {
	for _, typ := range []byte{Data, Ping, Pong} {
		seen := map[int]bool{}
		for i := 0; i < 2000; i++ {
			f := Frame{Type: typ, ID: 7}
			if typ == Data {
				f.Total = 1100
				f.Payload = bytes.Repeat([]byte{42}, 1100)
			}
			b, err := Encode(f)
			if err != nil {
				t.Fatal(err)
			}
			n := len(b) - Header - len(f.Payload)
			if n < 1 || n > 20 || int(b[2]) != n {
				t.Fatal("padding length", n)
			}
			seen[n] = true
			got, err := Decode(b)
			if err != nil || !bytes.Equal(got.Payload, f.Payload) {
				t.Fatal("padding leaked into payload", err)
			}
			bad := append([]byte(nil), b...)
			bad[2] = 0
			if _, err = Decode(bad); err == nil {
				t.Fatal("zero padding accepted")
			}
		}
		if len(seen) != 20 {
			t.Fatal("padding lengths missing", seen)
		}
	}
}
