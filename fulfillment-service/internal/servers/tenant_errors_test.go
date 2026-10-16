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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("convertTenantErrorToGRPC", func() {
	var testCtx context.Context

	BeforeEach(func() {
		testCtx = context.Background()
	})

	Context("with nil error", func() {
		It("should return nil", func() {
			result := convertTenantErrorToGRPC(testCtx, nil, logger, "engineering")
			Expect(result).ToNot(HaveOccurred())
		})
	})

	Context("with TenantInvisibleError", func() {
		It("should return PermissionDenied with appropriate message", func() {
			err := &auth.TenantInvisibleError{Tenant: "engineering"}

			result := convertTenantErrorToGRPC(testCtx, err, logger, "engineering")

			Expect(result).To(HaveOccurred())
			status, ok := grpcstatus.FromError(result)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			Expect(status.Message()).To(Equal("you are not authorized to use the specified tenant"))
		})
	})

	Context("with TenantUnassignableError", func() {
		It("should return PermissionDenied with appropriate message", func() {
			err := &auth.TenantUnassignableError{Tenant: "sales"}

			result := convertTenantErrorToGRPC(testCtx, err, logger, "sales")

			Expect(result).To(HaveOccurred())
			status, ok := grpcstatus.FromError(result)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			Expect(status.Message()).To(Equal("you are not authorized to use the specified tenant"))
		})
	})

	Context("with unknown error", func() {
		It("should return Internal error with generic message", func() {
			err := errors.New("some unexpected error")

			result := convertTenantErrorToGRPC(testCtx, err, logger, "development")

			Expect(result).To(HaveOccurred())
			status, ok := grpcstatus.FromError(result)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Internal))
			Expect(status.Message()).To(Equal("failed to determine tenant"))
		})
	})
})
