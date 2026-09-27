// Package proto defines unreliable, message-oriented tunnel frames.
package proto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
)

const (
	Header   = 16
	MaxInner = 9000
	Data     = 1
	Ping     = 2
	Pong     = 3
)

var ErrFrame = errors.New("invalid tunnel frame")

type Frame struct {
	Type          byte
	ID            uint64
	Total, Offset int
	Payload       []byte
}

func (f Frame) Valid() bool {
	if f.Type == Ping || f.Type == Pong {
		return f.Total == 0 && f.Offset == 0 && len(f.Payload) == 0
	}
	return f.Type == Data && f.ID != 0 && f.Total > 0 && f.Total <= MaxInner && f.Offset >= 0 && f.Offset < f.Total && len(f.Payload) > 0 && len(f.Payload) <= f.Total-f.Offset
}
func Decode(b []byte) (Frame, error) {
	if len(b) < Header || b[0] != 2 || b[2] < 1 || b[2] > 20 || b[3] != 0 || len(b) < Header+int(b[2]) {
		return Frame{}, ErrFrame
	}
	f := Frame{b[1], binary.BigEndian.Uint64(b[4:12]), int(binary.BigEndian.Uint16(b[12:14])), int(binary.BigEndian.Uint16(b[14:16])), b[16 : len(b)-int(b[2])]}
	if !f.Valid() {
		return Frame{}, ErrFrame
	}
	return f, nil
}
func Encode(f Frame) ([]byte, error) {
	if !f.Valid() {
		return nil, ErrFrame
	}
	// Rejection sampling keeps lengths uniform over 1..20. Padding and its
	// length field are authenticated/encrypted together with the frame by DTLS.
	var random [21]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	for random[0] >= 240 {
		if _, err := rand.Read(random[:1]); err != nil {
			return nil, err
		}
	}
	pad := int(random[0]%20) + 1
	b := make([]byte, Header+len(f.Payload)+pad)
	b[0] = 2
	b[2] = byte(pad)
	copy(b[Header+len(f.Payload):], random[1:1+pad])
	b[1] = f.Type
	binary.BigEndian.PutUint64(b[4:12], f.ID)
	binary.BigEndian.PutUint16(b[12:14], uint16(f.Total))
	binary.BigEndian.PutUint16(b[14:16], uint16(f.Offset))
	copy(b[16:], f.Payload)
	return b, nil
}
func Fragment(id uint64, p []byte, max int, emit func([]byte) error) error {
	if len(p) == 0 || len(p) > MaxInner || max < 1 || max > 1100 {
		return ErrFrame
	}
	for off := 0; off < len(p); off += max {
		end := off + max
		if end > len(p) {
			end = len(p)
		}
		b, e := Encode(Frame{Data, id, len(p), off, p[off:end]})
		if e != nil {
			return e
		}
		if e = emit(b); e != nil {
			return e
		}
	}
	return nil
}

type IDs struct{ Value uint64 }

func NewIDs() (*IDs, error) {
	var b [8]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, e
	}
	return &IDs{binary.BigEndian.Uint64(b[:])&(math.MaxUint64>>1) + 1}, nil
}
func (i *IDs) Next() (uint64, error) {
	if i.Value == math.MaxUint64 {
		return 0, errors.New("packet ID exhausted; reconnect")
	}
	i.Value++
	return i.Value, nil
}

// ValidIP checks only the L3 envelope, never parses or modifies TCP/UDP.
func ValidIP(p []byte) bool {
	if len(p) < 20 || len(p) > MaxInner {
		return false
	}
	switch p[0] >> 4 {
	case 4:
		return int(p[0]&15)*4 >= 20 && int(p[0]&15)*4 <= len(p) && int(binary.BigEndian.Uint16(p[2:4])) == len(p)
	case 6:
		return len(p) >= 40 && int(binary.BigEndian.Uint16(p[4:6]))+40 == len(p)
	}
	return false
}
