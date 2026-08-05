/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package masks

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Calculator", func() {
	var calculator *Calculator

	BeforeEach(func() {
		calculator = NewCalculator().Build()
	})

	Context("when no fields have changed", func() {
		It("should return an empty field mask", func() {
			finalizers := []string{"controller"}
			template := "template-1"

			before := privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Finalizers: finalizers,
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: template,
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Finalizers: finalizers,
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: template,
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(BeEmpty())
		})
	})

	Context("when scalar fields change", func() {
		It("should detect state change", func() {
			before := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_READY,
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.state"))
		})

		It("should detect string field change", func() {
			before := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					Template: "template-1",
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					Template: "template-2",
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("spec.template"))
		})

		It("should detect hub field addition", func() {
			before := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
					Hub:   "hub-1",
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.hub"))
			Expect(mask.Paths).NotTo(ContainElement("status.state"))
		})

		It("should detect multiple scalar changes", func() {
			before := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
					Hub:   "hub-1",
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_READY,
					Hub:   "hub-2",
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.state"))
			Expect(mask.Paths).To(ContainElement("status.hub"))
		})
	})

	Context("when list fields change", func() {
		It("should detect finalizer addition", func() {
			before := privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"controller"},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("metadata.finalizers"))
		})

		It("should detect condition list changes", func() {
			reason1 := "Reason1"
			message1 := "Message1"
			reason2 := "Reason2"
			message2 := "Message2"

			before := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{
						privatev1.ClusterCondition_builder{
							Type:    privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
							Status:  privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
							Reason:  &reason1,
							Message: &message1,
						}.Build(),
					},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{
						privatev1.ClusterCondition_builder{
							Type:    privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
							Status:  privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
							Reason:  &reason2,
							Message: &message2,
						}.Build(),
					},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.conditions"))
		})

		It("should detect condition list length change", func() {
			reason := "Reason1"
			message := "Message1"

			before := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{
						privatev1.ClusterCondition_builder{
							Type:    privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
							Status:  privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
							Reason:  &reason,
							Message: &message,
						}.Build(),
					},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.conditions"))
		})
	})

	Context("when map fields change", func() {
		It("should detect node set size change", func() {
			before := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     3,
						}.Build(),
					},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     5,
						}.Build(),
					},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("spec.node_sets"))
		})

		It("should detect node set key addition", func() {
			before := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     3,
						}.Build(),
					},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     3,
						}.Build(),
						"storage": privatev1.ClusterNodeSet_builder{
							HostType: "storage-type",
							Size:     2,
						}.Build(),
					},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("spec.node_sets"))
		})

		It("should detect node set key removal", func() {
			before := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     3,
						}.Build(),
						"storage": privatev1.ClusterNodeSet_builder{
							HostType: "storage-type",
							Size:     2,
						}.Build(),
					},
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							HostType: "worker-type",
							Size:     3,
						}.Build(),
					},
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("spec.node_sets"))
		})
	})

	Context("when multiple fields change at different levels", func() {
		It("should detect all changed fields", func() {
			template := "template-1"

			before := privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{},
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: template,
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			hub := "hub-1"
			after := privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"controller"},
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: template,
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_READY,
					Hub:   hub,
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("metadata.finalizers"))
			Expect(mask.Paths).To(ContainElement("status.state"))
			Expect(mask.Paths).To(ContainElement("status.hub"))
			Expect(mask.Paths).NotTo(ContainElement("spec.template"))
			Expect(mask.Paths).NotTo(ContainElement("id"))
		})
	})

	Context("when fields are cleared", func() {
		It("should detect cleared sub-message field", func() {
			before := privatev1.Tenant_builder{
				Status: privatev1.TenantStatus_builder{
					State:         privatev1.TenantState_TENANT_STATE_PENDING,
					IdpTenantName: "my-tenant",
					BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
						Username: "my-tenant-osac-break-glass",
						Password: "secret",
					}.Build(),
				}.Build(),
			}.Build()

			after := privatev1.Tenant_builder{
				Status: privatev1.TenantStatus_builder{
					State:         privatev1.TenantState_TENANT_STATE_SYNCED,
					IdpTenantName: "my-tenant",
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status.state"))
			Expect(mask.Paths).To(ContainElement("status.break_glass_credentials"))
		})

		It("should detect cleared top-level message field", func() {
			before := privatev1.Cluster_builder{
				Id: "test-cluster",
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			after := privatev1.Cluster_builder{
				Id: "test-cluster",
			}.Build()

			mask := calculator.Calculate(before, after)
			Expect(mask.Paths).To(ContainElement("status"))
		})
	})

	Context("when transitioning from empty to populated", func() {
		It("should detect all new fields at top level", func() {
			before := privatev1.Cluster_builder{}.Build()

			after := privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"controller"},
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build()

			mask := calculator.Calculate(before, after)
			// For newly added fields, only the top-level paths are included
			// since field masks are hierarchical (e.g., "metadata" includes all metadata sub-fields)
			Expect(mask.Paths).To(ContainElement("id"))
			Expect(mask.Paths).To(ContainElement("metadata"))
			Expect(mask.Paths).To(ContainElement("status"))
			Expect(mask.Paths).NotTo(ContainElement("metadata.finalizers"))
			Expect(mask.Paths).NotTo(ContainElement("status.state"))
		})
	})
})
