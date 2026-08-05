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
	"errors"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Token credentials", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	Describe("Creation", func() {
		It("Can be created with all mandatory parameters", func() {
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				SetSource(NewMockTokenSource(ctrl)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			credentials, err := NewTokenCredentials().
				SetSource(NewMockTokenSource(ctrl)).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(credentials).To(BeNil())
		})

		It("Can't be created without a source", func() {
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("source is mandatory"))
			Expect(credentials).To(BeNil())
		})
	})

	Describe("Behavior", func() {
		It("Adds Authorization header to metadata", func() {
			// Create a token source that returns a valid token:
			source := NewMockTokenSource(ctrl)
			source.EXPECT().Token(gomock.Any()).
				Return(
					&Token{
						Access: "my-token",
					},
					nil,
				).
				AnyTimes()

			// Create the credentials:
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				SetSource(source).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the metadata:
			metadata, err := credentials.GetRequestMetadata(ctx, "localhost:8000")
			Expect(err).ToNot(HaveOccurred())
			Expect(metadata).ToNot(BeNil())
			Expect(metadata).To(HaveKeyWithValue("Authorization", "Bearer my-token"))
		})

		It("Returns error when token source fails", func() {
			// Create a token source that returns an error:
			source := NewMockTokenSource(ctrl)
			source.EXPECT().Token(gomock.Any()).
				Return(
					nil,
					errors.New("my error"),
				).
				AnyTimes()

			// Create the credentials:
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				SetSource(source).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the metadata:
			metadata, err := credentials.GetRequestMetadata(ctx, "localhost:8000")
			Expect(err).To(MatchError("my error"))
			Expect(metadata).To(BeNil())
		})

		It("Requires transport security", func() {
			// Create the credentials:
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				SetSource(NewMockTokenSource(ctrl)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the transport security:
			security := credentials.RequireTransportSecurity()
			Expect(security).To(BeTrue())
		})

		It("Passes context to token source", func() {
			// Set up mock expectations with context verification:
			source := NewMockTokenSource(ctrl)
			source.EXPECT().Token(ctx).
				Return(
					&Token{
						Access: "my-token",
					},
					nil,
				).
				AnyTimes()

			// Create the credentials:
			credentials, err := NewTokenCredentials().
				SetLogger(logger).
				SetSource(source).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify the metadata:
			_, err = credentials.GetRequestMetadata(ctx, "localhost:8000")
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
