/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Static token source", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Creation", func() {
		It("Can be created with all the mandatory parameters", func() {
			token := &Token{
				Access: "my-token",
			}
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				SetToken(token).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(source).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			token := &Token{
				Access: "my-token",
			}
			source, err := NewStaticTokenSource().
				SetToken(token).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(source).To(BeNil())
		})

		It("Can't be created without a token", func() {
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("token is mandatory"))
			Expect(source).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Returns the configured token", func() {
			// Create the token:
			expectedToken := &Token{
				Access: "my-static-token",
			}

			// Create the source:
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				SetToken(expectedToken).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the token is returned:
			token, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).ToNot(BeNil())
			Expect(token).To(Equal(expectedToken))
			Expect(token.Access).To(Equal("my-static-token"))
		})

		It("Returns the same token on multiple calls", func() {
			// Create the token:
			expectedToken := &Token{
				Access: "my-static-token",
			}

			// Create the source:
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				SetToken(expectedToken).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the same token is returned on multiple calls:
			token1, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token1).To(Equal(expectedToken))

			token2, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token2).To(Equal(expectedToken))

			token3, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token3).To(Equal(expectedToken))
		})

		It("Returns a token with expiry information", func() {
			// Create a token with expiry:
			expiryTime := time.Now().Add(5 * time.Minute)
			expectedToken := &Token{
				Access: MakeTokenString("https://example.com/auth/realms/my", "Bearer", 5*time.Minute),
				Expiry: expiryTime,
			}

			// Create the source:
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				SetToken(expectedToken).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the token is returned with the expiry:
			token, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).ToNot(BeNil())
			Expect(token.Access).To(Equal(expectedToken.Access))
			Expect(token.Expiry).To(Equal(expiryTime))
		})

		It("Returns a token with refresh token", func() {
			// Create a token with a refresh token:
			expectedToken := &Token{
				Access:  "my-access-token",
				Refresh: "my-refresh-token",
			}

			// Create the source:
			source, err := NewStaticTokenSource().
				SetLogger(logger).
				SetToken(expectedToken).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the token is returned with the refresh token:
			token, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).ToNot(BeNil())
			Expect(token.Access).To(Equal("my-access-token"))
			Expect(token.Refresh).To(Equal("my-refresh-token"))
		})
	})
})
