/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("ToGrpcError", func() {
	It("reports the provider status for responses without a dedicated mapping", func() {
		err := ToGrpcError(generateResponseError(http.StatusBadGateway, "sensitive-provider-response"))
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Internal))
		Expect(status.Message()).To(Equal("vault operation failed (HTTP 502)"))
		Expect(status.Message()).ToNot(ContainSubstring("sensitive-provider-response"))
	})
})

var _ = Describe("ErrorDetail", func() {
	It("names the failed step, the tenant and the provider status", func() {
		err := newProvisioningError("failed to create policy", "tenant-a",
			generateResponseError(http.StatusConflict, "path is already in use at secret/"))
		Expect(ErrorDetail(err)).To(Equal(`failed to create policy for tenant "tenant-a" (HTTP 409)`))
	})

	It("omits the provider response body", func() {
		err := newProvisioningError("failed to create role", "tenant-a",
			generateResponseError(http.StatusBadRequest, "synthetic-provider-body-marker"))
		Expect(ErrorDetail(err)).ToNot(ContainSubstring("synthetic-provider-body-marker"))
	})

	It("omits the provider status when the response has none", func() {
		err := newProvisioningError("failed to create role", "tenant-a", fmt.Errorf("connection refused"))
		Expect(ErrorDetail(err)).To(Equal(`failed to create role for tenant "tenant-a"`))
	})

	It("returns nothing for errors that may reproduce a response body", func() {
		// Login errors embed the provider response, so they are not log safe.
		err := fmt.Errorf("vault login returned status 403: synthetic-body synthetic-token")
		Expect(ErrorDetail(err)).To(BeEmpty())
	})

	It("returns nothing without an error", func() {
		Expect(ErrorDetail(nil)).To(BeEmpty())
	})
})
