package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func writeTestCertificate(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "metrics.test.svc"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"metrics.test.svc"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der},
	), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(
		&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER},
	), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func TestMetricsCertificateTLSOpts(t *testing.T) {
	base := func(*tls.Config) {}

	t.Run("uses the mounted certificate when present", func(t *testing.T) {
		dir := t.TempDir()
		writeTestCertificate(t, dir)
		opts, watcher := metricsCertificateTLSOpts(
			dir, "tls.crt", "tls.key", []func(*tls.Config){base}, logr.Discard(),
		)
		if watcher == nil {
			t.Fatal("expected a certificate watcher")
		}
		if got, want := len(opts), 2; got != want {
			t.Fatalf("got %d TLS options, want %d", got, want)
		}
	})

	t.Run("falls back when the certificate is missing", func(t *testing.T) {
		opts, watcher := metricsCertificateTLSOpts(
			filepath.Join(t.TempDir(), "missing"), "tls.crt", "tls.key",
			[]func(*tls.Config){base}, logr.Discard(),
		)
		if watcher != nil {
			t.Fatal("expected no certificate watcher")
		}
		if got, want := len(opts), 1; got != want {
			t.Fatalf("got %d TLS options, want %d", got, want)
		}
	})

	t.Run("does nothing without a certificate path", func(t *testing.T) {
		opts, watcher := metricsCertificateTLSOpts(
			"", "tls.crt", "tls.key", []func(*tls.Config){base}, logr.Discard(),
		)
		if watcher != nil {
			t.Fatal("expected no certificate watcher")
		}
		if got, want := len(opts), 1; got != want {
			t.Fatalf("got %d TLS options, want %d", got, want)
		}
	})
}
