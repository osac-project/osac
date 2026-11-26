// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test write CA"},
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
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca *testCA) issue(t *testing.T, cn string, serial int64, notBefore, notAfter time.Time, usages ...x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	if len(usages) == 0 {
		usages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("issuing certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing issued certificate: %v", err)
	}
	return cert
}

func syncedStore() *RevocationStore {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	return store
}

var testNow = time.Now()

func TestVerifyPeerAcceptsTrustedClient(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	identity, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if err != nil {
		t.Fatalf("VerifyPeer: %v", err)
	}
	if identity.Cluster != "cluster-a" {
		t.Errorf("cluster = %q, want cluster-a", identity.Cluster)
	}
}

func TestVerifyPeerRejectsMissingCertificate(t *testing.T) {
	ca := newTestCA(t)
	_, err := VerifyPeer(nil, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("err = %v, want ErrNoCertificate", err)
	}
}

func TestVerifyPeerRejectsUntrustedChain(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	leaf := other.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrUntrustedChain) {
		t.Fatalf("err = %v, want ErrUntrustedChain", err)
	}
}

func TestVerifyPeerRejectsExpiredCertificate(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-2*time.Hour), testNow.Add(-time.Hour))

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestVerifyPeerRejectsRevokedSerial(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: leaf.SerialNumber.Text(16)})

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), store, testNow)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v, want ErrRevoked", err)
	}
}

func TestVerifyPeerRejectsManagementIdentity(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, ManagementCluster, 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrManagementIdentity) {
		t.Fatalf("err = %v, want ErrManagementIdentity", err)
	}
}

func TestVerifyPeerRejectsUnsyncedStore(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), NewRevocationStore(), testNow)
	if !errors.Is(err, ErrRevocationUnsynced) {
		t.Fatalf("err = %v, want ErrRevocationUnsynced", err)
	}
}

func TestVerifyPeerRequiresClientAuthUsage(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour), x509.ExtKeyUsageServerAuth)

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrUntrustedChain) {
		t.Fatalf("err = %v, want ErrUntrustedChain", err)
	}
}

// OSAC-8002: revocation is by serial, so a replacement certificate issued
// under the same common name is accepted while the serial it replaced stays
// revoked. This fails if verification ever starts denying a common name.
func TestVerifyPeerAcceptsNewSerialForTheSameCommonName(t *testing.T) {
	ca := newTestCA(t)
	replaced := ca.issue(t, "cluster-a", 1, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	replacement := ca.issue(t, "cluster-a", 2, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: replaced.SerialNumber.Text(16)})

	identity, err := VerifyPeer([]*x509.Certificate{replacement}, ca.pool(), store, testNow)
	if err != nil {
		t.Fatalf("VerifyPeer for the replacement serial: %v", err)
	}
	if identity.Cluster != "cluster-a" {
		t.Errorf("cluster = %q, want cluster-a", identity.Cluster)
	}
	_, err = VerifyPeer([]*x509.Certificate{replaced}, ca.pool(), store, testNow)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("err for the replaced serial = %v, want ErrRevoked", err)
	}
}

func TestVerifyPeerRejectsEmptyCommonName(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	_, err := VerifyPeer([]*x509.Certificate{leaf}, ca.pool(), syncedStore(), testNow)
	if !errors.Is(err, ErrEmptyIdentity) {
		t.Fatalf("err = %v, want ErrEmptyIdentity", err)
	}
}
