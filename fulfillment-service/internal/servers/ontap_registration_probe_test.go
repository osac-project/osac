/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestOntapRegistrationProbe(t *testing.T) {
	paths := []string{"/api/cluster", "/api/svm/svms", "/api/network/ip/interfaces",
		"/api/network/fc/interfaces", "/api/storage/qos/policies", "/api/storage/volumes", "/api/storage/luns"}
	t.Run("bounded authenticated discovery including empty collections", func(t *testing.T) {
		var seen []string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.URL.Path)
			user, password, ok := r.BasicAuth()
			if r.Method != http.MethodGet || !ok || user != "discovery" || password != testPassword {
				t.Error("expected authenticated GET")
			}
			if r.URL.Path == "/api/cluster" {
				if r.URL.Query().Get("fields") != "version" {
					t.Error("expected version-only query")
				}
				fmt.Fprint(w, `{"version":{"full":"NetApp Release 9.17.1"}}`)
			} else {
				query := r.URL.Query()
				if query.Get("fields") != "uuid" || query.Get("max_records") != "1" || query.Get("return_timeout") != "2" {
					t.Error("expected bounded discovery query")
				}
				fmt.Fprint(w, `{"records":[],"num_records":0}`)
			}
		}))
		defer srv.Close()
		err := NewOntapRegistrationProbe(srv.Client()).Probe(context.Background(), srv.URL+"/", "discovery", testPassword)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(seen, ",") != strings.Join(paths, ",") {
			t.Fatalf("unexpected calls: %v", seen)
		}
	})
	for _, tc := range []struct {
		name     string
		httpCode int
		body     string
		code     codes.Code
	}{
		{"unauthorized", 401, testPassword, codes.InvalidArgument},
		{"forbidden", 403, testPassword, codes.FailedPrecondition},
		{"unsupported", 404, testPassword, codes.FailedPrecondition},
		{"server failure", 500, testPassword, codes.Unavailable},
		{"redirect", 302, testPassword, codes.Unavailable},
		{"invalid JSON", 200, testPassword, codes.FailedPrecondition},
		{"missing version", 200, `{}`, codes.FailedPrecondition},
		{"oversized", 200, strings.Repeat("x", (1<<20)+1), codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(tc.httpCode)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			err := NewOntapRegistrationProbe(srv.Client()).Probe(context.Background(), srv.URL, "discovery", testPassword)
			if status.Code(err) != tc.code {
				t.Fatalf("got %v, want %v", err, tc.code)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Fatal("error exposed response/credentials")
			}
		})
	}
	t.Run("redirect never forwards credentials", func(t *testing.T) {
		calls := 0
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			http.Redirect(w, r, "/target", http.StatusFound)
		}))
		defer srv.Close()
		err := NewOntapRegistrationProbe(srv.Client()).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.Unavailable || calls != 1 {
			t.Fatalf("redirect followed: calls=%d, err=%v", calls, err)
		}
	})
	t.Run("system roots reject untrusted TLS", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("credentials sent over untrusted connection") }))
		defer srv.Close()
		err := NewOntapRegistrationProbe(nil).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid endpoint never calls server", func(t *testing.T) {
		for _, endpoint := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/api", "https://example.com?fields=*", "https://example.com#fragment", "https://", "https://example.com:invalid"} {
			err := NewOntapRegistrationProbe(nil).Probe(context.Background(), endpoint, "discovery", testPassword)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("endpoint %s: %v", endpoint, err)
			}
		}
	})
	t.Run("deadline and cancellation", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		err := NewOntapRegistrationProbe(srv.Client()).Probe(ctx, srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("got %v", err)
		}
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		err = NewOntapRegistrationProbe(srv.Client()).Probe(ctx, srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.Canceled {
			t.Fatalf("got %v", err)
		}
	})
}

func TestOntapRegistrationProbeCertificateChecks(t *testing.T) {
	t.Run("trusted certificate with wrong identity", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("credentials sent to wrong certificate identity") }))
		defer srv.Close()
		client := srv.Client()
		transport := client.Transport.(*http.Transport).Clone()
		transport.TLSClientConfig.ServerName = "wrong.invalid"
		client.Transport = transport
		err := NewOntapRegistrationProbe(client).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("trusted expired certificate", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(-time.Hour),
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		roots.AddCert(parsed)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("credentials sent with expired certificate") }))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
		srv.StartTLS()
		defer srv.Close()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
		err = NewOntapRegistrationProbe(client).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid collection shape", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/cluster" {
				fmt.Fprint(w, `{"version":{"full":"9.17.1"}}`)
			} else {
				fmt.Fprint(w, `{"records":[{}],"num_records":0}`)
			}
		}))
		defer srv.Close()
		err := NewOntapRegistrationProbe(srv.Client()).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ten second total budget", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
			if r.URL.Path == "/api/cluster" {
				fmt.Fprint(w, `{"version":{"full":"9.17.1"}}`)
			} else {
				fmt.Fprint(w, `{"records":[],"num_records":0}`)
			}
		}))
		defer srv.Close()
		start := time.Now()
		err := NewOntapRegistrationProbe(srv.Client()).Probe(context.Background(), srv.URL, "discovery", testPassword)
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("got %v", err)
		}
		if duration := time.Since(start); duration > 12*time.Second {
			t.Fatalf("total budget exceeded: %s", duration)
		}
	})

}
