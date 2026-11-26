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

package metricswrite

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func testCertificatePEM(t *testing.T) ([]byte, *big.Int) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x4a1b2c3d),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), template.SerialNumber
}

func TestParseSerialReturnsLowercaseHex(t *testing.T) {
	certPEM, want := testCertificatePEM(t)
	got, err := ParseSerial(certPEM)
	if err != nil {
		t.Fatalf("parse serial: %v", err)
	}
	if got != want.Text(16) {
		t.Errorf("serial = %q, want %q", got, want.Text(16))
	}
}

func TestParseSerialRejectsInvalidPEM(t *testing.T) {
	tests := map[string][]byte{
		"empty":       nil,
		"garbage":     []byte("not a certificate"),
		"wrong block": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}}),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSerial(raw); err == nil {
				t.Fatal("parse succeeded, want an error")
			}
		})
	}
}

func TestAppendSerial(t *testing.T) {
	tests := map[string]struct {
		current string
		serial  string
		want    string
	}{
		"empty list":              {current: "", serial: "abc", want: "abc"},
		"blank key":               {current: "\n", serial: "abc", want: "abc"},
		"append after one":        {current: "abc", serial: "def", want: "abc\ndef"},
		"append after trailing":   {current: "abc\n", serial: "def", want: "abc\ndef"},
		"deduplicate":             {current: "abc", serial: "abc", want: "abc"},
		"deduplicate case folded": {current: "ABC", serial: "abc", want: "ABC"},
		"drop blank lines":        {current: "abc\n\ndef", serial: "ghi", want: "abc\ndef\nghi"},
		"trim surrounding space":  {current: " abc ", serial: " def ", want: "abc\ndef"},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := AppendSerial(testCase.current, testCase.serial)
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			if got != testCase.want {
				t.Errorf("append(%q, %q) = %q, want %q", testCase.current, testCase.serial, got, testCase.want)
			}
		})
	}
}

func TestAppendSerialRejectsEmptySerial(t *testing.T) {
	if _, err := AppendSerial("abc", "  "); err == nil {
		t.Fatal("append succeeded, want an error")
	}
}
