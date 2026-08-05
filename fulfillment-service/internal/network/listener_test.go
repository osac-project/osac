/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"

	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Listener", func() {
	var tmp string

	BeforeEach(func() {
		var err error
		tmp, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		err := os.RemoveAll(tmp)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Can't be created without a logger", func() {
		listener, err := NewListener().
			SetAddress("127.0.0.1:0").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(listener).To(BeNil())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("logger"))
		Expect(msg).To(ContainSubstring("mandatory"))
	})

	It("Can't be created without an address", func() {
		listener, err := NewListener().
			SetLogger(logger).
			Build()
		Expect(err).To(HaveOccurred())
		Expect(listener).To(BeNil())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("address"))
		Expect(msg).To(ContainSubstring("mandatory"))
	})

	It("Can't be created with an incorrect address", func() {
		listener, err := NewListener().
			SetLogger(logger).
			SetAddress("junk").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(listener).To(BeNil())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("junk"))
	})

	It("Uses the given address", func() {
		address := filepath.Join(tmp, "my.socket")
		listener, err := NewListener().
			SetLogger(logger).
			SetNetwork("unix").
			SetAddress(address).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(listener).ToNot(BeNil())
		Expect(listener.Addr().String()).To(Equal(address))
	})

	It("Honors the address flag", func() {
		// Prepare the flags:
		address := filepath.Join(tmp, "my.socket")
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddListenerFlags(flags, "my", "localhost:80")
		err := flags.Parse([]string{
			"--my-listener-address", address,
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the listener:
		listener, err := NewListener().
			SetLogger(logger).
			SetFlags(flags, "my").
			SetNetwork("unix").
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(listener).ToNot(BeNil())
		Expect(listener.Addr().String()).To(Equal(address))
	})

	It("Ignores flags for other listeners", func() {
		// Prepare the flags:
		myAddress := filepath.Join(tmp, "my.socket")
		yourAddress := filepath.Join(tmp, "your.socket")
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddListenerFlags(flags, "my", "localhost:80")
		AddListenerFlags(flags, "your", "localhost:81")
		err := flags.Parse([]string{
			"--my-listener-address", myAddress,
			"--your-listener-address", yourAddress,
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the listener:
		listener, err := NewListener().
			SetLogger(logger).
			SetFlags(flags, "my").
			SetNetwork("unix").
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(listener).ToNot(BeNil())
		Expect(listener.Addr().String()).To(Equal(myAddress))
	})

	Context("TLS enabled", func() {
		var (
			crtRaw, keyRaw   []byte
			crtPEM, keyPEM   []byte
			crtFile, keyFile string
		)

		BeforeEach(func() {
			// Generate the key pair and the self signed certificate:
			crt := testing.LocalhostCertificate()
			crtRaw = crt.Certificate[0]

			// Write the certificate file:
			crtPEM = pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: crtRaw,
			})
			crtFile = filepath.Join(tmp, "tls.crt")
			err := os.WriteFile(crtFile, crtPEM, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Write the key bytes:
			keyRaw, err = x509.MarshalPKCS8PrivateKey(crt.PrivateKey)
			Expect(err).ToNot(HaveOccurred())
			keyPEM = pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: keyRaw,
			})
			keyFile = filepath.Join(tmp, "tls.key")
			err = os.WriteFile(keyFile, keyPEM, 0600)
			Expect(err).ToNot(HaveOccurred())
		})

		// serve waits for a connection, closes it and returns. Note that in order to complete the TLS handshake
		// we need to _try_ to read something, even if the client isn't going to send anything inside of the TLS
		// envelope.
		serve := func(listener net.Listener) {
			conn, err := listener.Accept()
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()
			_, err = conn.Read([]byte{})
			Expect(err).To(Or(Not(HaveOccurred()), Equal(io.EOF)))
		}

		// check opens a connection to the given listener and verifies that it uses the given certificate.
		check := func(listener net.Listener, crt []byte) {
			cas := x509.NewCertPool()
			ok := cas.AppendCertsFromPEM(crtPEM)
			Expect(ok).To(BeTrue())
			dialer := tls.Dialer{
				Config: &tls.Config{
					RootCAs: cas,
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			addr := listener.Addr()
			conn, err := dialer.DialContext(ctx, addr.Network(), addr.String())
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := conn.Close()
				Expect(err).ToNot(HaveOccurred())
			}()
			tlsConn := conn.(*tls.Conn)
			err = tlsConn.Handshake()
			Expect(err).ToNot(HaveOccurred())
			crts := tlsConn.ConnectionState().PeerCertificates
			Expect(crts[0].Raw).To(Equal(crtRaw))
		}

		It("Supports TLS", func() {
			// Create the listener:
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				SetTLSKey(keyFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := listener.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Start listening and serve only the first connection request, as that is all we need to
			// perform the verification.
			go func() {
				defer GinkgoRecover()
				serve(listener)
			}()

			// Check the certificate:
			check(listener, crtRaw)
		})

		It("Supports TLS flags", func() {
			// Prepare the flags:
			flags := pflag.NewFlagSet("", pflag.ContinueOnError)
			AddListenerFlags(flags, "my", "localhost:80")
			err := flags.Parse([]string{
				"--my-listener-address", "127.0.0.1:0",
				"--my-listener-tls-crt", crtFile,
				"--my-listener-tls-key", keyFile,
			})
			Expect(err).ToNot(HaveOccurred())

			// Create the listener:
			listener, err := NewListener().
				SetLogger(logger).
				SetFlags(flags, "my").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer listener.Close()

			// Check the server certificate:
			go func() {
				defer GinkgoRecover()
				serve(listener)
			}()
			check(listener, crtRaw)
		})

		It("Can't be created if TLS certificate is set but key isn't", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("key"))
			Expect(msg).To(ContainSubstring("mandatory"))
		})

		It("Can't be created if TLS key is set but certificate isn't", func() {
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSKey(keyFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("certificate"))
			Expect(msg).To(ContainSubstring("mandatory"))
		})

		It("Can't be created if TLS certificate file doesn't exist", func() {
			missingFile := filepath.Join(tmp, "missing.crt")
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(missingFile).
				SetTLSKey(keyFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("missing.crt"))
			Expect(msg).To(ContainSubstring("no such file"))
		})

		It("Can't be created if TLS key file doesn't exist", func() {
			missingFile := filepath.Join(tmp, "missing.key")
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				SetTLSKey(missingFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("missing.key"))
			Expect(msg).To(ContainSubstring("no such file"))
		})

		It("Can't be created if TLS certificate file contains junk", func() {
			junkFile := filepath.Join(tmp, "junk.pem")
			err := os.WriteFile(junkFile, []byte("junk\n"), 0600)
			Expect(err).ToNot(HaveOccurred())
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(junkFile).
				SetTLSKey(keyFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("failed to find any PEM data in certificate"))
		})

		It("Can't be created if TLS key file contains junk", func() {
			junkFile := filepath.Join(tmp, "junk.pem")
			err := os.WriteFile(junkFile, []byte("junk\n"), 0600)
			Expect(err).ToNot(HaveOccurred())
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				SetTLSKey(junkFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(listener).To(BeNil())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("failed to find any PEM data in key"))
		})

		It("Can be used to create an HTTP1 server", func() {
			// Create the listener:
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				SetTLSKey(keyFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer listener.Close()

			// Create the server:
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			server := http.Server{
				Handler: handler,
			}
			go func() {
				defer GinkgoRecover()
				_ = server.Serve(listener)
			}()
			defer func() {
				_ = server.Shutdown(context.Background())
			}()

			// Send a request to verify that it supports HTTP1:
			cas := x509.NewCertPool()
			ok := cas.AppendCertsFromPEM(crtPEM)
			Expect(ok).To(BeTrue())
			client := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs: cas,
					},
				},
			}
			response, err := client.Get("https://" + listener.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.Proto).To(Equal("HTTP/1.1"))
		})

		It("Can be used to create an HTTP2 server", func() {
			// Create the listener:
			listener, err := NewListener().
				SetLogger(logger).
				SetAddress("127.0.0.1:0").
				SetTLSCrt(crtFile).
				SetTLSKey(keyFile).
				AddTLSProtocol("h2").
				Build()
			Expect(err).ToNot(HaveOccurred())
			defer listener.Close()

			// Create the server:
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			server := http.Server{
				Handler: handler,
			}
			go func() {
				defer GinkgoRecover()
				_ = server.Serve(listener)
			}()
			defer func() {
				_ = server.Shutdown(context.Background())
			}()

			// Send a request to verify that it supports HTTP2:
			cas := x509.NewCertPool()
			ok := cas.AppendCertsFromPEM(crtPEM)
			Expect(ok).To(BeTrue())
			client := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs: cas,
					},
					ForceAttemptHTTP2: true,
				},
			}
			response, err := client.Get("https://" + listener.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.Proto).To(Equal("HTTP/2.0"))
		})
	})
})
