/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
)

// MakeTCPServer creates a test server that listens in a TCP socket and configured so that it sends log messages to the
// Ginkgo writer.
func MakeTCPServer() *ghttp.Server {
	server := ghttp.NewUnstartedServer()
	server.Writer = GinkgoWriter
	server.HTTPTestServer.Config.ErrorLog = log.New(GinkgoWriter, "", log.LstdFlags)
	server.HTTPTestServer.EnableHTTP2 = true
	server.HTTPTestServer.Start()
	return server
}

// MakeTCPTLSServer creates a test server configured so that it sends log messages to the Ginkgo writer. It returns the
// created server and the name of a temporary file that contains the CA certificate that the client should trust in
// order to connect to the server. It is the responsibility of the caller to delete this temporary file when it is no
// longer needed.
func MakeTCPTLSServer() (server *ghttp.Server, ca string) {
	// Create and configure the server:
	server = ghttp.NewUnstartedServer()
	server.Writer = GinkgoWriter
	server.HTTPTestServer.Config.ErrorLog = log.New(GinkgoWriter, "", log.LstdFlags)
	server.HTTPTestServer.EnableHTTP2 = true
	server.HTTPTestServer.StartTLS()

	// Fetch the CA certificate:
	address, err := url.Parse(server.URL())
	Expect(err).ToNot(HaveOccurred())
	ca = fetchCACertificate("tcp", address.Host)

	return
}

// fetchCACertificates connects to the given network address and extracts the CA certificate from the TLS handshake. It
// returns the path of a temporary file containing that CA certificate encoded in PEM format. It is the responsibility
// of the caller to delete that file when it is no longer needed.
func fetchCACertificate(network, address string) string {
	// Connect to the server and do the TLS handshake to obtain the certificate chain:
	conn, err := tls.Dial(network, address, &tls.Config{
		InsecureSkipVerify: true,
	})
	Expect(err).ToNot(HaveOccurred())
	defer func() {
		err = conn.Close()
		Expect(err).ToNot(HaveOccurred())
	}()
	err = conn.Handshake()
	Expect(err).ToNot(HaveOccurred())
	certs := conn.ConnectionState().PeerCertificates
	Expect(certs).ToNot(BeNil())
	Expect(certs).ToNot(BeEmpty())
	cert := certs[len(certs)-1]
	Expect(cert).ToNot(BeNil())

	// Serialize the CA certificate:
	Expect(cert.Raw).ToNot(BeNil())
	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	}
	buffer := pem.EncodeToMemory(block)
	Expect(buffer).ToNot(BeNil())

	// Store the CA certificate in a temporary file:
	file, err := os.CreateTemp("", "*.test.ca")
	Expect(err).ToNot(HaveOccurred())
	_, err = file.Write(buffer)
	Expect(err).ToNot(HaveOccurred())
	err = file.Close()
	Expect(err).ToNot(HaveOccurred())

	// Return the path of the temporary file:
	return file.Name()
}

// RespondeWithContent responds with the given status code, content type and body.
func RespondWithContent(status int, contentType, body string) http.HandlerFunc {
	return ghttp.RespondWith(
		status,
		body,
		http.Header{
			"Content-Type": []string{
				contentType,
			},
		},
	)
}

// RespondWithJSON responds with the given status code and JSON body.
func RespondWithJSON(status int, body string) http.HandlerFunc {
	return RespondWithContent(status, "application/json", body)
}

// RespondWithObject returns an HTTP handler that responds with a single object.
func RespondWithObject(object any) http.HandlerFunc {
	return ghttp.RespondWithJSONEncoded(http.StatusOK, object)
}

// LocalhostCertificate returns a self signed TLS certificate valid for the name `localhost` DNS name, for the
// `127.0.0.1` IPv4 address and for the `::1` IPv6 address.
//
// A similar certificate can be generated with the following command:
//
//	openssl req \
//	-x509 \
//	-newkey rsa:4096 \
//	-nodes \
//	-keyout tls.key \
//	-out tls.crt \
//	-subj '/CN=localhost' \
//	-addext 'subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1' \
//	-days 1
func LocalhostCertificate() tls.Certificate {
	if localhostCertificate == nil {
		key, err := rsa.GenerateKey(rand.Reader, 4096)
		Expect(err).ToNot(HaveOccurred())
		now := time.Now()
		spec := x509.Certificate{
			SerialNumber: big.NewInt(0),
			Subject: pkix.Name{
				CommonName: "localhost",
			},
			DNSNames: []string{
				"localhost",
			},
			IPAddresses: []net.IP{
				net.ParseIP("127.0.0.1"),
				net.ParseIP("::1"),
			},
			NotBefore: now,
			NotAfter:  now.Add(24 * time.Hour),
			KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{
				x509.ExtKeyUsageServerAuth,
			},
		}
		data, err := x509.CreateCertificate(rand.Reader, &spec, &spec, &key.PublicKey, key)
		Expect(err).ToNot(HaveOccurred())
		localhostCertificate = &tls.Certificate{
			Certificate: [][]byte{data},
			PrivateKey:  key,
		}
	}
	return *localhostCertificate
}

// LocalhostCertificateFiles returns the paths to three temporary files containing the certificate, private key and
// CA certificate returned by LocalhostCertificate. All files are PEM encoded. Since the certificate is self-signed,
// the CA file contains the same certificate. It is the responsibility of the caller to delete these files when they
// are no longer needed.
func LocalhostCertificateFiles() (certFile, keyFile, caFile string) {
	cert := LocalhostCertificate()

	// Encode the certificate to PEM format:
	certBlock := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Certificate[0],
	}
	certData := pem.EncodeToMemory(certBlock)
	Expect(certData).ToNot(BeNil())

	// Write the certificate to a temporary file:
	certTmp, err := os.CreateTemp("", "*.test.crt")
	Expect(err).ToNot(HaveOccurred())
	_, err = certTmp.Write(certData)
	Expect(err).ToNot(HaveOccurred())
	err = certTmp.Close()
	Expect(err).ToNot(HaveOccurred())
	certFile = certTmp.Name()

	// Encode the private key to PEM format and write it to a temporary file:
	keyBytes, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	Expect(err).ToNot(HaveOccurred())
	keyBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	}
	keyData := pem.EncodeToMemory(keyBlock)
	Expect(keyData).ToNot(BeNil())
	keyTmp, err := os.CreateTemp("", "*.test.key")
	Expect(err).ToNot(HaveOccurred())
	_, err = keyTmp.Write(keyData)
	Expect(err).ToNot(HaveOccurred())
	err = keyTmp.Close()
	Expect(err).ToNot(HaveOccurred())
	keyFile = keyTmp.Name()

	// Write the CA certificate to a temporary file (same as the certificate since it is self-signed):
	caTmp, err := os.CreateTemp("", "*.test.ca")
	Expect(err).ToNot(HaveOccurred())
	_, err = caTmp.Write(certData)
	Expect(err).ToNot(HaveOccurred())
	err = caTmp.Close()
	Expect(err).ToNot(HaveOccurred())
	caFile = caTmp.Name()

	return
}

// localhostCertificate contains the TLS certificate returned by the LocalhostCertificate function.
var localhostCertificate *tls.Certificate
