/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ = Describe("ClassifyHubError", func() {
	Context("with nil error", func() {
		It("should return nil", func() {
			result := ClassifyHubError(nil)
			Expect(result).ToNot(HaveOccurred())
		})
	})

	Context("with NotFound error", func() {
		It("should return ErrHubNotFound", func() {
			grpcErr := status.Error(codes.NotFound, "hub not found in database")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeTrue())
		})
	})

	Context("with transient errors", func() {
		It("should return original error for Unavailable", func() {
			grpcErr := status.Error(codes.Unavailable, "connection refused")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(HaveOccurred())
		})

		It("should return original error for DeadlineExceeded", func() {
			grpcErr := status.Error(codes.DeadlineExceeded, "timeout")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(HaveOccurred())
		})

		It("should return original error for Internal", func() {
			grpcErr := status.Error(codes.Internal, "internal server error")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(HaveOccurred())
		})
	})

	Context("with non-gRPC error", func() {
		It("should return the original error unchanged", func() {
			nonGrpcErr := errors.New("generic error")

			result := ClassifyHubError(nonGrpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(Equal(nonGrpcErr))
		})
	})

	Context("with auth-related errors", func() {
		It("should treat PermissionDenied as retryable", func() {
			// PermissionDenied is not transient (won't resolve with retries) but not NotFound either.
			// This test documents that it's intentionally treated as retryable to allow for
			// temporary RBAC issues or credential rotation scenarios.
			grpcErr := status.Error(codes.PermissionDenied, "permission denied")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(HaveOccurred())
			Expect(result).To(Equal(grpcErr))
		})

		It("should treat Unauthenticated as retryable", func() {
			// Unauthenticated is not transient (won't resolve with retries) but not NotFound either.
			// This test documents that it's intentionally treated as retryable to allow for
			// token refresh or credential rotation scenarios.
			grpcErr := status.Error(codes.Unauthenticated, "authentication failed")

			result := ClassifyHubError(grpcErr)

			Expect(errors.Is(result, ErrHubNotFound)).To(BeFalse())
			Expect(result).To(HaveOccurred())
			Expect(result).To(Equal(grpcErr))
		})
	})
})
