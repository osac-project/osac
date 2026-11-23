// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/osac-project/osac/osac-observability/internal/writeproxy"
)

// writeServerCertificate writes a self-signed serving key pair with the given
// serial, replacing whatever was there.
func writeServerCertificate(t *testing.T, certFile, keyFile string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating serving key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "write-proxy"},
		DNSNames:     []string{"write-proxy"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating serving certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling serving key: %v", err)
	}
	writeFile(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// tenantCertificate is the certificate a tenant presents. The proxy only
// requires that one was presented, so a self-signed pair is enough here.
func tenantCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating tenant key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "tenant"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating tenant certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// peerSerial dials the listener once and reports the serial of the certificate
// the server presented.
func peerSerial(t *testing.T, address string, certificate tls.Certificate) int64 {
	t.Helper()
	connection, err := tls.Dial("tcp", address, &tls.Config{ // #nosec G402 -- test dial
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatalf("dialing %s: %v", address, err)
	}
	defer func() { _ = connection.Close() }()
	return connection.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

// serveOnce accepts one connection, completes the handshake against cfg and
// reports the outcome. It runs before the matching dial.
func serveOnce(listener net.Listener, cfg *tls.Config) <-chan error {
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = connection.Close() }()
		done <- tls.Server(connection, cfg).Handshake()
	}()
	return done
}

func TestReloadingCertificateServesARenewedCertificate(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	writeServerCertificate(t, certFile, keyFile, 100)

	certificate, err := newReloadingCertificate(certFile, keyFile)
	if err != nil {
		t.Fatalf("newReloadingCertificate: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	first := serveOnce(listener, serverTLSConfig(certificate))
	serial := peerSerial(t, listener.Addr().String(), tenantCertificate(t))
	if err := <-first; err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	if serial != 100 {
		t.Fatalf("first handshake presented serial %d, want 100", serial)
	}

	// cert-manager replaces the mounted key pair; the next handshake must use it
	// without a restart.
	writeServerCertificate(t, certFile, keyFile, 200)
	second := serveOnce(listener, serverTLSConfig(certificate))
	serial = peerSerial(t, listener.Addr().String(), tenantCertificate(t))
	if err := <-second; err != nil {
		t.Fatalf("second handshake: %v", err)
	}
	if serial != 200 {
		t.Fatalf("handshake after renewal presented serial %d, want 200", serial)
	}
}

type testAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestAuthority(t *testing.T) *testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "write CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	return &testAuthority{cert: cert, key: key}
}

func (ca *testAuthority) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

func (ca *testAuthority) issue(t *testing.T, commonName string, serial int64) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating client key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("issuing client certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing issued certificate: %v", err)
	}
	return cert
}

func syncedRevocationStore() *writeproxy.RevocationStore {
	store := writeproxy.NewRevocationStore()
	store.Apply(map[string]string{writeproxy.RevocationKey: ""})
	return store
}

func TestReloadingCertPoolFollowsTheWriteCA(t *testing.T) {
	firstCA := newTestAuthority(t)
	secondCA := newTestAuthority(t)
	firstLeaf := firstCA.issue(t, "cluster-a", 11)
	secondLeaf := secondCA.issue(t, "cluster-b", 22)
	store := syncedRevocationStore()

	path := filepath.Join(t.TempDir(), "ca.crt")
	writeFile(t, path, firstCA.pem())
	pool, err := newReloadingCertPool(path)
	if err != nil {
		t.Fatalf("newReloadingCertPool: %v", err)
	}

	if _, err := writeproxy.VerifyPeer([]*x509.Certificate{firstLeaf}, pool.Pool(), store, time.Now()); err != nil {
		t.Fatalf("write under the current CA: %v", err)
	}
	_, err = writeproxy.VerifyPeer([]*x509.Certificate{secondLeaf}, pool.Pool(), store, time.Now())
	if !errors.Is(err, writeproxy.ErrUntrustedChain) {
		t.Fatalf("write under the next CA = %v, want ErrUntrustedChain before the swap", err)
	}

	// cert-manager replaces the bundle; make the change observable even when the
	// new file has the same size.
	writeFile(t, path, secondCA.pem())
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("setting bundle mtime: %v", err)
	}

	if _, err := writeproxy.VerifyPeer([]*x509.Certificate{secondLeaf}, pool.Pool(), store, time.Now()); err != nil {
		t.Fatalf("write under the rotated CA was rejected: %v", err)
	}
	_, err = writeproxy.VerifyPeer([]*x509.Certificate{firstLeaf}, pool.Pool(), store, time.Now())
	if !errors.Is(err, writeproxy.ErrUntrustedChain) {
		t.Fatalf("write under the previous CA = %v, want ErrUntrustedChain after the swap", err)
	}
}
