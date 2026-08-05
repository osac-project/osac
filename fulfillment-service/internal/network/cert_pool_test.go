/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Certificate pool", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error

		// Create a temporary directory:
		tmpDir, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	// makeCertsResult is the result of the function that makes certificates.
	type makeCertsResult struct {
		caCert      *x509.Certificate
		caCertFile  string
		caKey       *rsa.PrivateKey
		caKeyFile   string
		tlsCert     *x509.Certificate
		tlsCertFile string
		tlsKey      *rsa.PrivateKey
		tlsKeyFile  string
	}

	// makeCerts creates a CA certificate and a TLS certificate signed by that CA. Returns the certificate objects.
	// and their corresponding file paths for tests that need files.
	makeCerts := func(name string) makeCertsResult {
		// Generate the CA key pair:
		caPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).ToNot(HaveOccurred())
		caPublicKey := &caPrivateKey.PublicKey

		// Create the CA certificate template:
		caDate := time.Now()
		caCertTemplate := x509.Certificate{
			SerialNumber: big.NewInt(0),
			Subject: pkix.Name{
				CommonName: name,
			},
			NotBefore:             caDate,
			NotAfter:              caDate.AddDate(10, 0, 0),
			IsCA:                  true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			BasicConstraintsValid: true,
			MaxPathLen:            2,
		}

		// Sign the CA certificate:
		caDer, err := x509.CreateCertificate(rand.Reader, &caCertTemplate, &caCertTemplate, caPublicKey, caPrivateKey)
		Expect(err).ToNot(HaveOccurred())

		// Parse the CA certificate object:
		caCert, err := x509.ParseCertificate(caDer)
		Expect(err).ToNot(HaveOccurred())

		// PEM encode the CA certificate:
		caPem := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: caDer,
		})

		// Save the CA certificate to a temporary file:
		caCertFile := filepath.Join(tmpDir, name+".pem")
		err = os.WriteFile(caCertFile, caPem, 0600)
		Expect(err).ToNot(HaveOccurred())

		// Generate the TLS key pair:
		tlsPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).ToNot(HaveOccurred())
		tlsPublicKey := &tlsPrivateKey.PublicKey

		// Create the TLS certificate template:
		tlsCertTemplate := x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				CommonName: "localhost",
			},
			NotBefore: caDate,
			NotAfter:  caDate.AddDate(10, 0, 0),
			KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: []x509.ExtKeyUsage{
				x509.ExtKeyUsageServerAuth,
			},
			BasicConstraintsValid: true,
			DNSNames:              []string{"localhost"},
			IPAddresses: []net.IP{
				net.ParseIP("127.0.0.1"),
				net.ParseIP("::1"),
			},
		}

		// Sign the TLS certificate with the CA:
		tlsDer, err := x509.CreateCertificate(rand.Reader, &tlsCertTemplate, &caCertTemplate, tlsPublicKey, caPrivateKey)
		Expect(err).ToNot(HaveOccurred())

		// Parse the TLS certificate object:
		tlsCert, err := x509.ParseCertificate(tlsDer)
		Expect(err).ToNot(HaveOccurred())

		// PEM encode the TLS certificate:
		tlsPem := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: tlsDer,
		})

		// Save the TLS certificate to a temporary file:
		tlsCertFile := filepath.Join(tmpDir, name+"-tls.pem")
		err = os.WriteFile(tlsCertFile, tlsPem, 0600)
		Expect(err).ToNot(HaveOccurred())

		// PEM encode the TLS private key:
		tlsKeyPem := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(tlsPrivateKey),
		})

		// Save the TLS private key to a temporary file:
		tlsKeyFile := filepath.Join(tmpDir, name+"-tls-key.pem")
		err = os.WriteFile(tlsKeyFile, tlsKeyPem, 0600)
		Expect(err).ToNot(HaveOccurred())

		return makeCertsResult{
			caCert:      caCert,
			caCertFile:  caCertFile,
			caKey:       caPrivateKey,
			caKeyFile:   caCertFile,
			tlsCert:     tlsCert,
			tlsCertFile: tlsCertFile,
			tlsKey:      tlsPrivateKey,
			tlsKeyFile:  tlsKeyFile,
		}
	}

	Describe("Creation", func() {
		It("Fails if logger is not set", func() {
			pool, err := NewCertPool().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(pool).To(BeNil())
		})

		It("Can be created without adding any files", func() {
			pool, err := NewCertPool().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with one file", func() {
			// Create one CA file:
			myCerts := makeCerts("My CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddFile(myCerts.caCertFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with multiple files", func() {
			// Create two CA files:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddFiles(myCerts.caCertFile, yourCerts.caCertFile).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can't be created with files that don't exist", func() {
			// Create a path for a flie that doesn't exist:
			doesNotExist := filepath.Join(tmpDir, "does-not-exist.pem")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddFile(doesNotExist).
				Build()
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("no such file or directory"))
			Expect(pool).To(BeNil())
		})

		It("Can't be created with files that don't contain valid certificates", func() {
			// Create a file with invalid content:
			junkFile := filepath.Join(tmpDir, "junk.pem")
			err := os.WriteFile(junkFile, []byte("junk"), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddFile(junkFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("doesn't contain any CA certificate"))
			Expect(pool).To(BeNil())
		})

		It("Can be created with a single certificate from bytes", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			caPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate(caPem).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with a single certificate from a string", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			caPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate(string(caPem)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with a single certificate from X.509 object", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate(myCerts.caCert).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with multiple certificates from bytes", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")
			myPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			yourPem, err := os.ReadFile(yourCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificates(myPem, yourPem).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with multiple certificates from strings", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")
			myPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			yourPem, err := os.ReadFile(yourCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificates(string(myPem), string(yourPem)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Can be created with multiple certificates from X.509 objects", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificates(myCerts.caCert, yourCerts.caCert).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Fails with invalid byte content", func() {
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate([]byte("junk")).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add certificate"))
			Expect(pool).To(BeNil())
		})

		It("Fails with invalid string content", func() {
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate("not a valid certificate").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add certificate"))
			Expect(pool).To(BeNil())
		})

		It("Fails with unsupported type", func() {
			pool, err := NewCertPool().
				SetLogger(logger).
				AddCertificate(12345).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(
				"invalid certificate type 'int', should be 'string', '[]byte' or '*x509.Certificate'",
			))
			Expect(pool).To(BeNil())
		})
	})

	Describe("Behavior", func() {
		It("Can verify certificates using the certificate pool", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")

			// Create the pool that only contains the generated CA certificate:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddFile(myCerts.caCertFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			chains, err := myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(chains).ToNot(BeEmpty())
		})

		It("Can verify certificates from multiple CAs using the certificate pool", func() {
			// Create certificates from two different CAs:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create the pool that contains both CA certificates:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddFiles(myCerts.caCertFile, yourCerts.caCertFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify both TLS certificates using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Can load certificates from subdirectories recursively", func() {
			// Create certificates from two different CAs:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create a directory structure for testing recursive loading:
			certDir := filepath.Join(tmpDir, "certificates")
			err := os.MkdirAll(certDir, 0755)
			Expect(err).ToNot(HaveOccurred())

			subDir := filepath.Join(certDir, "subdirectory")
			err = os.MkdirAll(subDir, 0755)
			Expect(err).ToNot(HaveOccurred())

			// Copy the first CA certificate to the main directory:
			mainCertPath := filepath.Join(certDir, "main-ca.pem")
			mainCertData, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(mainCertPath, mainCertData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Copy the second CA certificate to the subdirectory:
			subCertPath := filepath.Join(subDir, "sub-ca.pem")
			subCertData, err := os.ReadFile(yourCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(subCertPath, subCertData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool that loads certificates from the directory recursively:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddFile(certDir).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify both TLS certificates using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Ignores files with unknown extensions when loading from directories", func() {
			// Create certificates from two different CAs:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create a directory for testing extension filtering:
			certDir := filepath.Join(tmpDir, "mixed-extensions")
			err := os.MkdirAll(certDir, 0755)
			Expect(err).ToNot(HaveOccurred())

			// Copy the first CA certificate with a valid extension (.pem):
			validCertPath := filepath.Join(certDir, "valid-ca.pem")
			validCertData, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(validCertPath, validCertData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Copy the second CA certificate with an invalid extension (.txt):
			invalidCertPath := filepath.Join(certDir, "invalid-ca.txt")
			invalidCertData, err := os.ReadFile(yourCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(invalidCertPath, invalidCertData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool that loads certificates from the directory:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddFile(certDir).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the first TLS certificate (should succeed - CA was loaded):
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())

			// Verify the second TLS certificate (should fail - CA was ignored due to .txt extension):
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("certificate signed by unknown authority"))
		})

		It("Loads Kubernetes certificates", func() {
			// Create certificates from two different CAs:
			kubeCerts := makeCerts("Kubernetes CA")

			// Create the Kubernetes directory structure in the temporary directory:
			saDir := filepath.Join(tmpDir, "var", "run", "secrets", "kubernetes.io", "serviceaccount")
			err := os.MkdirAll(saDir, 0755)
			Expect(err).ToNot(HaveOccurred())

			// Copy the CA certificate to the standard Kubernetes CA location:
			caPath := filepath.Join(saDir, "ca.crt")
			caData, err := os.ReadFile(kubeCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(caPath, caData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool with root pointing to our temporary directory:
			pool, err := NewCertPool().
				SetLogger(logger).
				SetRoot(tmpDir).
				AddSystemFiles(false).
				AddKubernetesFiles(true).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = kubeCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Loads OpenShift service certificates", func() {
			// Create certificates from two different CAs:
			serviceCerts := makeCerts("Service CA")

			// Create the Kubernetes directory structure in the temporary directory:
			saDir := filepath.Join(tmpDir, "var", "run", "secrets", "kubernetes.io", "serviceaccount")
			err := os.MkdirAll(saDir, 0755)
			Expect(err).ToNot(HaveOccurred())

			// Copy the CA certificate to the standard OpenShift service CA location:
			caPath := filepath.Join(saDir, "service-ca.crt")
			caData, err := os.ReadFile(serviceCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(caPath, caData, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool with root pointing to our temporary directory:
			pool, err := NewCertPool().
				SetLogger(logger).
				SetRoot(tmpDir).
				AddSystemFiles(false).
				AddKubernetesFiles(true).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = serviceCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Can verify certificates loaded from a byte slice", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			caPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificate(caPem).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			chains, err := myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(chains).ToNot(BeEmpty())
		})

		It("Can verify certificates loaded from a string", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			caPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificate(string(caPem)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			chains, err := myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(chains).ToNot(BeEmpty())
		})

		It("Can verify certificates loaded from an X.509 object", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificate(myCerts.caCert).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificate using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			chains, err := myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(chains).ToNot(BeEmpty())
		})

		It("Can verify certificates from multiple CAs loaded from byte slices", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")
			myPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())
			yourPem, err := os.ReadFile(yourCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificates(myPem, yourPem).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificates using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Can verify certificates from multiple CAs loaded from X.509 objects", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificates(myCerts.caCert, yourCerts.caCert).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificates using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Can mix byte slices and X.509 objects in the same pool", func() {
			// Create the certificates:
			myCerts := makeCerts("My CA")
			yourCerts := makeCerts("Your CA")
			myPem, err := os.ReadFile(myCerts.caCertFile)
			Expect(err).ToNot(HaveOccurred())

			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				AddKubernetesFiles(false).
				AddCertificates(myPem, yourCerts.caCert).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the TLS certificates using the pool:
			opts := x509.VerifyOptions{
				Roots: pool,
			}
			_, err = myCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
			_, err = yourCerts.tlsCert.Verify(opts)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Doesn't load system certificates if explicitly disabled", func() {
			// Create the pool:
			pool, err := NewCertPool().
				SetLogger(logger).
				AddSystemFiles(false).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Try to use a web site that uses a certificate that is signed by a well known CA that should
			// be trusted by any system, but will not be loaded, so it should fail.
			client := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs: pool,
					},
				},
			}
			request, err := http.NewRequest(http.MethodHead, "https://www.google.com", nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = client.Do(request)
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("certificate signed by unknown authority"))
		})
	})
})
