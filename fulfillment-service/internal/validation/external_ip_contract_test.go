/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package validation

import (
	"buf.build/go/protovalidate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ExternalIP contract validation", func() {
	var validator protovalidate.Validator

	BeforeEach(func() {
		var err error
		validator, err = protovalidate.New()
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("accepts valid attribution",
		func(attribution *privatev1.ExternalIPAttribution) {
			Expect(validator.Validate(attribution)).To(Succeed())
		},
		Entry("compute instance", privatev1.ExternalIPAttribution_builder{
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-id"}.Build(),
		}.Build()),
		Entry("cluster API", privatev1.ExternalIPAttribution_builder{
			Cluster:  privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
			Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		}.Build()),
		Entry("cluster ingress", privatev1.ExternalIPAttribution_builder{
			Cluster:  privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
			Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
		}.Build()),
		Entry("bare metal instance", privatev1.ExternalIPAttribution_builder{
			BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: "baremetal-id"}.Build(),
		}.Build()),
	)

	DescribeTable("rejects invalid attribution",
		func(attribution *privatev1.ExternalIPAttribution) {
			Expect(validator.Validate(attribution)).To(HaveOccurred())
		},
		Entry("missing target", privatev1.ExternalIPAttribution_builder{}.Build()),
		Entry("empty target id", privatev1.ExternalIPAttribution_builder{
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{}.Build(),
		}.Build()),
		Entry("cluster without endpoint", privatev1.ExternalIPAttribution_builder{
			Cluster: privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
		}.Build()),
		Entry("compute instance with endpoint", privatev1.ExternalIPAttribution_builder{
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-id"}.Build(),
			Endpoint:        privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		}.Build()),
		Entry("unknown endpoint", privatev1.ExternalIPAttribution_builder{
			Cluster:  privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
			Endpoint: privatev1.ExternalIPAttachmentEndpoint(99),
		}.Build()),
	)

	DescribeTable("rejects invalid attachment target endpoint",
		func(spec *privatev1.ExternalIPAttachmentSpec) {
			Expect(validator.Validate(spec)).To(HaveOccurred())
		},
		Entry("missing target", privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "external-ip-id"}.Build(),
		}.Build()),
		Entry("compute instance with endpoint", privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: "external-ip-id"}.Build(),
			ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-id"}.Build(),
			TargetEndpoint:  privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		}.Build()),
		Entry("cluster without endpoint", privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "external-ip-id"}.Build(),
			Cluster:    privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
		}.Build()),
	)
})
