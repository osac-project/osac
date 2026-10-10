/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookTLSConfigReloadsCertificate(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	keyFile := filepath.Join(filepath.Dir(certFile), "tls.key")
	writeWebhookCertificatePair(t, certFile, keyFile, 1)

	config := webhookTLSConfig(certFile, keyFile)
	first, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("load initial certificate: %v", err)
	}
	firstLeaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatalf("parse initial certificate: %v", err)
	}

	writeWebhookCertificatePair(t, certFile, keyFile, 2)
	second, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("load rotated certificate: %v", err)
	}
	secondLeaf, err := x509.ParseCertificate(second.Certificate[0])
	if err != nil {
		t.Fatalf("parse rotated certificate: %v", err)
	}
	if firstLeaf.SerialNumber.Cmp(secondLeaf.SerialNumber) == 0 {
		t.Fatal("serving certificate did not reload after the files changed")
	}
}

func writeWebhookCertificatePair(t *testing.T, certFile, keyFile string, serial int64) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal test key: %v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write test certificate: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write test key: %v", err)
	}
}
