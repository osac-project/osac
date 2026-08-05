/*
Copyright (c) 2025 Red Hat Inc.

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
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth/jwe"
)

var _ = Describe("JsonWebKeySetServer", func() {
	Describe("Build", func() {
		It("should fail without logger", func() {
			_, err := NewJsonWebKeySetServer().
				SetSealer(&jwe.Sealer{}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("should fail without sealer", func() {
			_, err := NewJsonWebKeySetServer().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("sealer"))
		})
	})

	Describe("Get", func() {
		It("should return a valid JWKS with one RSA signing key", func() {
			tmpDir, err := os.MkdirTemp("", "jwks-test-*")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			signingCertFile := tmpDir + "/signing-tls.crt"
			signingKeyFile := tmpDir + "/signing-tls.key"
			encryptionCertFile := tmpDir + "/encryption-tls.crt"
			encryptionKeyFile := tmpDir + "/encryption-tls.key"

			generateSelfSignedCert(signingCertFile, signingKeyFile, "test-signer")
			generateSelfSignedCert(encryptionCertFile, encryptionKeyFile, "test-encryption")
			_ = encryptionKeyFile

			sealer, err := jwe.NewSealer().
				SetLogger(logger).
				SetSigningCertFile(signingCertFile).
				SetSigningKeyFile(signingKeyFile).
				SetEncryptionCertFile(encryptionCertFile).
				SetIssuer("https://test.example.com").
				Build()
			Expect(err).NotTo(HaveOccurred())

			server, err := NewJsonWebKeySetServer().
				SetLogger(logger).
				SetSealer(sealer).
				Build()
			Expect(err).NotTo(HaveOccurred())

			resp, err := server.Get(context.Background(), publicv1.JsonWebKeySetGetRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.ContentType).To(Equal("application/json"))

			var jwksResp map[string]any
			Expect(json.Unmarshal(resp.Data, &jwksResp)).To(Succeed())

			keys, ok := jwksResp["keys"].([]any)
			Expect(ok).To(BeTrue(), "jwksResp[\"keys\"] should be []any, got %T", jwksResp["keys"])
			Expect(keys).To(HaveLen(1))
			key, ok := keys[0].(map[string]any)
			Expect(ok).To(BeTrue(), "keys[0] should be map[string]any, got %T", keys[0])
			Expect(key["kty"]).To(Equal("RSA"))
			Expect(key["use"]).To(Equal("sig"))
			Expect(key["alg"]).To(Equal("RS256"))
			Expect(key["kid"]).ToNot(BeEmpty())
		})
	})
})
