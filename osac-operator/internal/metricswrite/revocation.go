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
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// RevocationKey is the shared ConfigMap key that holds the newline-separated
// revoked serial numbers. The write proxy reads the same key.
const RevocationKey = "revoked"

// ParseSerial returns the lowercase hexadecimal serial number of the first
// certificate in a PEM bundle. The write proxy compares a presented
// certificate's serial in the same form, so a value appended here matches what
// the proxy checks.
func ParseSerial(certPEM []byte) (string, error) {
	for block, rest := pem.Decode(certPEM); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parse certificate: %w", err)
		}
		return certificate.SerialNumber.Text(16), nil
	}
	return "", errors.New("no certificate in PEM data")
}

// AppendSerial returns the newline-separated denylist with serial added. Blank
// lines are dropped, existing entries are trimmed, and a serial that is already
// present in any case is not duplicated.
func AppendSerial(current, serial string) (string, error) {
	trimmed := strings.TrimSpace(serial)
	if trimmed == "" {
		return "", errors.New("serial is required")
	}
	serials := make([]string, 0, strings.Count(current, "\n")+1)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(current, "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		normalized := strings.ToLower(entry)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		serials = append(serials, entry)
	}
	if _, exists := seen[strings.ToLower(trimmed)]; !exists {
		serials = append(serials, trimmed)
	}
	return strings.Join(serials, "\n"), nil
}
