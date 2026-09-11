/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Capabilities", func() {
	It("advertises enabled services over public and private gRPC", func(ctx context.Context) {
		expected := []string{"caas", "vmaas", "bmaas"}

		publicClient := publicv1.NewCapabilitiesClient(tool.ExternalView().AnonymousConn())
		publicResponse, err := publicClient.Get(ctx, publicv1.CapabilitiesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicResponse.GetEnabledServices()).To(Equal(expected))

		privateClient := privatev1.NewCapabilitiesClient(tool.InternalView().AdminConn())
		privateResponse, err := privateClient.Get(ctx, privatev1.CapabilitiesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateResponse.GetEnabledServices()).To(Equal(expected))
	})
})
