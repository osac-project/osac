/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Generic mapper", func() {
	parseDate := func(text string) *timestamppb.Timestamp {
		value, err := time.Parse(time.RFC3339, text)
		Expect(err).ToNot(HaveOccurred())
		return timestamppb.New(value)
	}

	DescribeTable(
		"Copy cluster private to public",
		func(from *privatev1.Cluster, to *publicv1.Cluster, expected *publicv1.Cluster) {
			mapper, err := NewGenericMapper[*privatev1.Cluster, *publicv1.Cluster]().
				SetLogger(logger).
				SetStrict(false).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = mapper.Copy(ctx, from, to)
			Expect(err).ToNot(HaveOccurred())
			marshalOptions := protojson.MarshalOptions{
				UseProtoNames: true,
			}
			actualJson, err := marshalOptions.Marshal(to)
			Expect(err).ToNot(HaveOccurred())
			expectedJson, err := marshalOptions.Marshal(expected)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualJson).To(MatchJSON(expectedJson))
		},
		Entry(
			"Nil",
			nil,
			nil,
			nil,
		),
		Entry(
			"Empty",
			&privatev1.Cluster{},
			&publicv1.Cluster{},
			&publicv1.Cluster{},
		),
		Entry(
			"Identifier",
			privatev1.Cluster_builder{
				Id: "123",
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Id: "123",
			}.Build(),
		),
		Entry(
			"Creation timestamp",
			privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
		),
		Entry(
			"Deletion timestamp",
			privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					DeletionTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					DeletionTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
		),
		Entry(
			"Empty spec",
			privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{}.Build(),
			}.Build(),
		),
		Entry(
			"Spec with node sets",
			privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"my-node-set": privatev1.ClusterNodeSet_builder{
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "my_bmit"}.Build(),
							Size:                  proto.Int32(123),
						}.Build(),
						"your-node-set": privatev1.ClusterNodeSet_builder{
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "your_bmit"}.Build(),
							Size:                  proto.Int32(456),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my-node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "my_bmit"}.Build(),
							Size:                  proto.Int32(123),
						}.Build(),
						"your-node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "your_bmit"}.Build(),
							Size:                  proto.Int32(456),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
		Entry(
			"Empty status",
			privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Status: publicv1.ClusterStatus_builder{}.Build(),
			}.Build(),
		),
		Entry(
			"Status with state",
			privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_READY,
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Status: publicv1.ClusterStatus_builder{
					State: publicv1.ClusterState_CLUSTER_STATE_READY,
				}.Build(),
			}.Build(),
		),
		Entry(
			"Status with one conditions",
			privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{
						privatev1.ClusterCondition_builder{
							Type:               privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							Status:             privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
							LastTransitionTime: parseDate("2025-06-02T14:53:00Z"),
							Reason:             new("MyReason"),
							Message:            new("My message."),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Status: publicv1.ClusterStatus_builder{
					Conditions: []*publicv1.ClusterCondition{
						publicv1.ClusterCondition_builder{
							Type:               publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							Status:             publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
							LastTransitionTime: parseDate("2025-06-02T14:53:00Z"),
							Reason:             new("MyReason"),
							Message:            new("My message."),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
		Entry(
			"Status with two conditions",
			privatev1.Cluster_builder{
				Status: privatev1.ClusterStatus_builder{
					Conditions: []*privatev1.ClusterCondition{
						privatev1.ClusterCondition_builder{
							Type:               privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							Status:             privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
							LastTransitionTime: parseDate("2025-06-02T14:53:00Z"),
							Reason:             new("MyReason"),
							Message:            new("My message."),
						}.Build(),
						privatev1.ClusterCondition_builder{
							Type:               privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_FAILED,
							Status:             privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
							LastTransitionTime: parseDate("2025-06-03T14:53:00Z"),
							Reason:             new("YourReason"),
							Message:            new("Your message."),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Status: publicv1.ClusterStatus_builder{
					Conditions: []*publicv1.ClusterCondition{
						publicv1.ClusterCondition_builder{
							Type:               publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							Status:             publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
							LastTransitionTime: parseDate("2025-06-02T14:53:00Z"),
							Reason:             new("MyReason"),
							Message:            new("My message."),
						}.Build(),
						publicv1.ClusterCondition_builder{
							Type:               publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_FAILED,
							Status:             publicv1.ConditionStatus_CONDITION_STATUS_FALSE,
							LastTransitionTime: parseDate("2025-06-03T14:53:00Z"),
							Reason:             new("YourReason"),
							Message:            new("Your message."),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
	)

	DescribeTable(
		"Merge cluster private to public",
		func(from *privatev1.Cluster, to *publicv1.Cluster, expected *publicv1.Cluster) {
			mapper, err := NewGenericMapper[*privatev1.Cluster, *publicv1.Cluster]().
				SetLogger(logger).
				SetStrict(false).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = mapper.Merge(ctx, from, to)
			Expect(err).ToNot(HaveOccurred())
			marshalOptions := protojson.MarshalOptions{
				UseProtoNames: true,
			}
			actualJson, err := marshalOptions.Marshal(to)
			Expect(err).ToNot(HaveOccurred())
			expectedJson, err := marshalOptions.Marshal(expected)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualJson).To(MatchJSON(expectedJson))
		},
		Entry(
			"Replace scalar field",
			privatev1.Cluster_builder{
				Id: "new-id",
			}.Build(),
			publicv1.Cluster_builder{
				Id: "old-id",
			}.Build(),
			publicv1.Cluster_builder{
				Id: "new-id",
			}.Build(),
		),
		Entry(
			"Merge into empty target",
			privatev1.Cluster_builder{
				Id: "123",
				Metadata: privatev1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Id: "123",
				Metadata: publicv1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
		),
		Entry(
			"Combine fields of nested messages",
			privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					DeletionTimestamp: parseDate("2025-06-02T15:00:00Z"),
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					CreationTimestamp: parseDate("2025-06-02T14:53:00Z"),
					DeletionTimestamp: parseDate("2025-06-02T15:00:00Z"),
				}.Build(),
			}.Build(),
		),
		Entry(
			"Merge entries of maps",
			privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"new-node-set": privatev1.ClusterNodeSet_builder{
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "new_bmit"}.Build(),
							Size:                  proto.Int32(789),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"existing-node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "existing_bmit"}.Build(),
							Size:                  proto.Int32(456),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"existing-node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "existing_bmit"}.Build(),
							Size:                  proto.Int32(456),
						}.Build(),
						"new-node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "new_bmit"}.Build(),
							Size:                  proto.Int32(789),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
		Entry(
			"Replace map entry",
			privatev1.Cluster_builder{
				Spec: privatev1.ClusterSpec_builder{
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"node-set": privatev1.ClusterNodeSet_builder{
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "updated_bmit"}.Build(),
							Size:                  proto.Int32(999),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "original_bmit"}.Build(),
							Size:                  proto.Int32(123),
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"node-set": publicv1.ClusterNodeSet_builder{
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "updated_bmit"}.Build(),
							Size:                  proto.Int32(999),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
	)

	It("omits reserved keys only from public Metadata without mutating the source", func() {
		labels := map[string]string{
			"admin.osac.openshift.io/managed":       "system",
			"provider.admin.osac.openshift.io/id":   "provider",
			"fakeadmin.osac.openshift.io/ordinary":  "ordinary",
			"admin.osac.openshift.io.example/value": "ordinary",
			"admin.osac.openshift.io":               "slashless",
			"Admin.osac.openshift.io/managed":       "mixed-case",
			"osac.openshift.io/tenant":              "tenant-a",
		}
		annotations := map[string]string{
			"admin.osac.openshift.io/managed":      "system",
			"provider.admin.osac.openshift.io/id":  "provider",
			"fakeadmin.osac.openshift.io/ordinary": "ordinary",
		}
		privateObject := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Labels:      labels,
				Annotations: annotations,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"admin.osac.openshift.io/ordinary-map": privatev1.ClusterNodeSet_builder{
						Size: proto.Int32(1),
					}.Build(),
				},
			}.Build(),
		}.Build()
		original := proto.Clone(privateObject)

		mapper, err := NewGenericMapper[*privatev1.Cluster, *publicv1.Cluster]().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicObject := &publicv1.Cluster{}
		Expect(mapper.Copy(ctx, privateObject, publicObject)).To(Succeed())
		Expect(publicObject.GetMetadata().GetLabels()).To(Equal(map[string]string{
			"fakeadmin.osac.openshift.io/ordinary":  "ordinary",
			"admin.osac.openshift.io.example/value": "ordinary",
			"admin.osac.openshift.io":               "slashless",
			"Admin.osac.openshift.io/managed":       "mixed-case",
			"osac.openshift.io/tenant":              "tenant-a",
		}))
		Expect(publicObject.GetMetadata().GetAnnotations()).To(Equal(map[string]string{
			"fakeadmin.osac.openshift.io/ordinary": "ordinary",
		}))
		Expect(publicObject.GetSpec().GetNodeSets()).To(HaveKey("admin.osac.openshift.io/ordinary-map"))
		Expect(proto.Equal(privateObject, original)).To(BeTrue())
	})

	It("restores reserved metadata from the current object", func() {
		current := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Labels: map[string]string{
					"admin.osac.openshift.io/managed":          "system-current",
					"operator.admin.osac.openshift.io/managed": "operator-current",
					"ordinary": "old",
				},
				Annotations: map[string]string{
					"admin.osac.openshift.io/owner": "system-current",
					"ordinary":                      "old",
				},
			}.Build(),
		}.Build()
		candidate := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Labels: map[string]string{
					"admin.osac.openshift.io/managed":          "stale-request-value",
					"operator.admin.osac.openshift.io/managed": "stale-operator-value",
					"ordinary": "updated",
					"new":      "added",
				},
				Annotations: map[string]string{"ordinary": "updated"},
			}.Build(),
		}.Build()
		originalCurrent := proto.Clone(current)

		restoreReservedMetadata(current, candidate)

		Expect(candidate.GetMetadata().GetLabels()).To(Equal(map[string]string{
			"admin.osac.openshift.io/managed":          "system-current",
			"operator.admin.osac.openshift.io/managed": "operator-current",
			"ordinary": "updated",
			"new":      "added",
		}))
		Expect(candidate.GetMetadata().GetAnnotations()).To(Equal(map[string]string{
			"admin.osac.openshift.io/owner": "system-current",
			"ordinary":                      "updated",
		}))
		Expect(proto.Equal(current, originalCurrent)).To(BeTrue())

		metadataOmittedCandidate := privatev1.Cluster_builder{}.Build()
		restoreReservedMetadata(current, metadataOmittedCandidate)
		Expect(metadataOmittedCandidate.GetMetadata().GetLabels()).To(Equal(map[string]string{
			"admin.osac.openshift.io/managed":          "system-current",
			"operator.admin.osac.openshift.io/managed": "operator-current",
		}))
		Expect(metadataOmittedCandidate.GetMetadata().GetAnnotations()).To(Equal(map[string]string{
			"admin.osac.openshift.io/owner": "system-current",
		}))
	})

	It("does not add omitted metadata when there are no reserved values to restore", func() {
		current := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Name:        "stored-cluster",
				Labels:      map[string]string{"ordinary": "stored"},
				Annotations: map[string]string{"ordinary": "stored"},
			}.Build(),
		}.Build()
		candidate := privatev1.Cluster_builder{}.Build()

		restoreReservedMetadata(current, candidate)

		Expect(candidate.GetMetadata()).To(BeNil())
	})

	It("rejects reserved metadata on public input before copying", func() {
		publicObject := publicv1.Cluster_builder{
			Id: "input-cluster",
			Metadata: publicv1.Metadata_builder{
				Name: "public-name",
				Labels: map[string]string{
					"admin.osac.openshift.io/managed":     "true",
					"provider.admin.osac.openshift.io/id": "provider-id",
				},
				Annotations: map[string]string{
					"admin.osac.openshift.io/owner": "system",
				},
			}.Build(),
		}.Build()
		privateObject := privatev1.Cluster_builder{
			Id: "unchanged-id",
			Metadata: privatev1.Metadata_builder{
				Name: "unchanged-name",
				Labels: map[string]string{
					"existing": "unchanged",
				},
			}.Build(),
		}.Build()
		originalSource := proto.Clone(publicObject)
		originalDestination := proto.Clone(privateObject)

		mapper, err := NewGenericMapper[*publicv1.Cluster, *privatev1.Cluster]().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		err = mapper.Copy(ctx, publicObject, privateObject)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(grpcstatus.Convert(err).Message()).To(Equal(reservedMetadataError))
		Expect(proto.Equal(publicObject, originalSource)).To(BeTrue())
		Expect(proto.Equal(privateObject, originalDestination)).To(BeTrue())
	})

	It("omits reserved metadata from public SelfSubjectAccessReview projections", func() {
		privateObject := privatev1.SelfSubjectAccessReview_builder{
			Metadata: privatev1.Metadata_builder{
				Labels: map[string]string{
					"admin.osac.openshift.io/review": "private",
					"ordinary":                       "visible",
				},
			}.Build(),
		}.Build()

		mapper, err := NewGenericMapper[*privatev1.SelfSubjectAccessReview, *publicv1.SelfSubjectAccessReview]().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicObject := &publicv1.SelfSubjectAccessReview{}
		Expect(mapper.Copy(ctx, privateObject, publicObject)).To(Succeed())
		Expect(publicObject.GetMetadata().GetLabels()).To(Equal(map[string]string{"ordinary": "visible"}))
		Expect(privateObject.GetMetadata().GetLabels()).To(HaveKeyWithValue("admin.osac.openshift.io/review", "private"))
	})
})
