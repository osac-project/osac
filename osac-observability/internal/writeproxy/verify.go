// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNoCertificate means the client did not present a certificate.
	ErrNoCertificate = errors.New("no client certificate")
	// ErrRevocationUnsynced means this replica has not yet observed the denylist, so it can
	// admit nothing.
	ErrRevocationUnsynced = errors.New("revocation list unavailable")
	// ErrEmptyIdentity means the certificate has no common name to stamp.
	ErrEmptyIdentity = errors.New("client certificate has no common name")
	// ErrManagementIdentity means a certificate claimed the direct-write management identity.
	ErrManagementIdentity = errors.New("management identity may not use the write proxy")
	// ErrExpired means the certificate is outside its validity period.
	ErrExpired = errors.New("client certificate is not currently valid")
	// ErrUntrustedChain means the certificate does not chain to the write CA for client auth.
	ErrUntrustedChain = errors.New("client certificate does not chain to the write CA")
	// ErrRevoked means the certificate's serial is on the shared denylist.
	ErrRevoked = errors.New("client certificate has been revoked")
)

// RevocationList is the part of RevocationStore that verification needs.
type RevocationList interface {
	Synced() bool
	Revoked(serial string) bool
}

// VerifyPeer establishes the identity of a remote-write client. It accepts only a certificate
// that chains to the write CA, is currently valid for client authentication, is not revoked,
// and names a cluster other than the management cluster. Any failure is reported with one of
// the sentinel errors above so the caller can answer 401 with a stable message.
func VerifyPeer(certs []*x509.Certificate, roots *x509.CertPool, revocation RevocationList, now time.Time) (Identity, error) {
	if revocation == nil || !revocation.Synced() {
		return Identity{}, ErrRevocationUnsynced
	}
	if len(certs) == 0 {
		return Identity{}, ErrNoCertificate
	}
	leaf := certs[0]
	if leaf.Subject.CommonName == "" {
		return Identity{}, ErrEmptyIdentity
	}
	if leaf.Subject.CommonName == ManagementCluster {
		return Identity{}, ErrManagementIdentity
	}

	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		var invalid x509.CertificateInvalidError
		if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
			return Identity{}, fmt.Errorf("%w: %v", ErrExpired, err)
		}
		return Identity{}, fmt.Errorf("%w: %v", ErrUntrustedChain, err)
	}
	if revocation.Revoked(leaf.SerialNumber.Text(16)) {
		return Identity{}, ErrRevoked
	}
	return Identity{Cluster: leaf.Subject.CommonName}, nil
}
