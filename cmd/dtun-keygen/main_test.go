package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir, "client"); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	pin, err := os.ReadFile(filepath.Join(dir, "client.pin"))
	if err != nil || strings.TrimSpace(string(pin)) != hex.EncodeToString(sum[:]) {
		t.Fatal("incorrect pin", err)
	}
	if err := generate(dir, "client"); err == nil {
		t.Fatal("overwrote identity")
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key")); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, "../escape"); err == nil {
		t.Fatal("accepted path traversal")
	}
}
