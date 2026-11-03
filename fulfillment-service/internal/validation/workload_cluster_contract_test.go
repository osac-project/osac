//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package validation

import (
	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("WorkloadCluster API contract", func() {
	It("requires a Hub identity, a DNS name, and at least one supported workload", func() {
		valid := publicv1.WorkloadCluster_builder{
			Metadata: publicv1.Metadata_builder{Name: "cluster-a"}.Build(),
			Spec: publicv1.WorkloadClusterSpec_builder{
				SourceHubId:        "hub-a",
				ManagedClusterName: "cluster-a",
				SupportedWorkloads: []string{"maas"},
			}.Build(),
		}.Build()
		Expect(protovalidate.Validate(valid)).To(Succeed())

		invalid := publicv1.WorkloadCluster_builder{
			Metadata: publicv1.Metadata_builder{Name: "cluster-a"}.Build(),
			Spec: publicv1.WorkloadClusterSpec_builder{
				SourceHubId:        "hub-a",
				ManagedClusterName: "Cluster-A",
			}.Build(),
		}.Build()
		Expect(protovalidate.Validate(invalid)).To(HaveOccurred())
	})

	It("keeps the observed Hub UID and reconciliation signal private", func() {
		Expect((&publicv1.WorkloadClusterStatus{}).ProtoReflect().Descriptor().Fields().ByName("managed_cluster_uid")).To(BeNil())
		Expect((&privatev1.WorkloadClusterStatus{}).ProtoReflect().Descriptor().Fields().ByName("managed_cluster_uid").Number()).To(Equal(protoreflect.FieldNumber(2)))
		Expect(publicv1.File_osac_public_v1_workload_clusters_service_proto.Services().ByName("WorkloadClusters").Methods().ByName("Signal")).To(BeNil())
		Expect(privatev1.File_osac_private_v1_workload_clusters_service_proto.Services().ByName("WorkloadClusters").Methods().ByName("Signal")).ToNot(BeNil())
	})
})
