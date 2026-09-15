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
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Resolved ClusterVersion lifecycle validation", func() {
	DescribeTable("uses the same status and source context for every caller",
		func(version *privatev1.ClusterVersion, reason string) {
			for _, source := range []string{"", " in fields.version"} {
				err := validateResolvedClusterVersion(version, "4.20", source)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("cluster version '4.20'" + source))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring(reason))
			}
		},
		Entry("deleted", privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build(),
			Spec:     privatev1.ClusterVersionSpec_builder{Enabled: new(true)}.Build(),
		}.Build(), "deleted"),
		Entry("disabled", privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{}.Build(),
			Spec:     privatev1.ClusterVersionSpec_builder{Enabled: new(false)}.Build(),
		}.Build(), "disabled"),
		Entry("obsolete", privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{}.Build(),
			Spec: privatev1.ClusterVersionSpec_builder{
				Enabled: new(true),
				State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
			}.Build(),
		}.Build(), "obsolete"),
	)
})
