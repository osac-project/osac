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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = DescribeTableSubtree("platform resource reference resolution", func(locked bool) {
	var instanceTypes *dao.GenericDAO[*privatev1.BareMetalInstanceType]

	BeforeEach(func() {
		var err error
		instanceTypes, err = dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		seedCaaSTestBareMetalInstanceType(ctx, "platform-type", "same-name", auth.SharedTenant, nil)
		seedCaaSTestBareMetalInstanceType(ctx, "tenant-type", "tenant-name", testTenant, nil)
	})

	DescribeTable("resolves only in platform scope", func(id, name string, expectedCode grpccodes.Code) {
		resolve := resolvePlatformResource[*privatev1.BareMetalInstanceType]
		if locked {
			resolve = resolveLockedPlatformResource[*privatev1.BareMetalInstanceType]
		}
		resolved, err := resolve(ctx, instanceTypes, id, name, "bare metal instance type", " in node set", grpccodes.NotFound)
		if expectedCode != grpccodes.OK {
			Expect(err).To(MatchError(ContainSubstring("bare metal instance type")))
			Expect(grpcstatus.Code(err)).To(Equal(expectedCode))
			Expect(resolved).To(BeNil())
			return
		}
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.GetId()).To(Equal("platform-type"))
		Expect(resolved.GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
		Expect(resolved.GetMetadata().GetProject()).To(BeEmpty())
	},
		Entry("by ID", "platform-type", "", grpccodes.OK),
		Entry("by name", "", "same-name", grpccodes.OK),
		Entry("by matching ID and name", "platform-type", "same-name", grpccodes.OK),
		Entry("rejects mismatched ID and name", "platform-type", "different-name", grpccodes.InvalidArgument),
		Entry("rejects a tenant ID despite caller visibility", "tenant-type", "", grpccodes.NotFound),
		Entry("rejects a tenant name despite caller visibility", "", "tenant-name", grpccodes.NotFound),
		Entry("rejects a missing name", "", "missing", grpccodes.NotFound),
		Entry("requires ID or name", "", "", grpccodes.InvalidArgument),
	)
},
	Entry("unlocked", false),
	Entry("locked", true),
)
