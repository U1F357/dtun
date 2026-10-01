// dtun-keygen creates a local identity without requiring OpenSSL.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

func main() {
	dir := flag.String("out-dir", "identity", "identity directory (restrict access to the private key)")
	name := flag.String("name", "client", "identity name: letters, digits, underscore, hyphen")
	flag.Parse()
	if err := generate(*dir, *name); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Created %s.{key,crt,pin} in %s. Exchange only .pin with your peer.\n", *name, *dir)
}

func generate(dir, name string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(name) {
		return fmt.Errorf("invalid identity name")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	pin := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	files := []struct {
		ext  string
		data []byte
	}{
		{"key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})},
		{"crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})},
		{"pin", []byte(hex.EncodeToString(pin[:]) + "\n")},
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var created []string
	complete := false
	defer func() {
		if !complete {
			for _, p := range created {
				_ = os.Remove(p)
			}
		}
	}()
	for _, file := range files {
		p := filepath.Join(dir, name+"."+file.ext)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		created = append(created, p)
		_, err = f.Write(file.data)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	complete = true
	return nil
}
