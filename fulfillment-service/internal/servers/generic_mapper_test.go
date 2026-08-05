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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
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
						"my_node_set": privatev1.ClusterNodeSet_builder{
							HostType: "my_host_type",
							Size:     123,
						}.Build(),
						"your_node_set": privatev1.ClusterNodeSet_builder{
							HostType: "your_host_type",
							Size:     456,
						}.Build(),
					},
				}.Build(),
			}.Build(),
			&publicv1.Cluster{},
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my_node_set": publicv1.ClusterNodeSet_builder{
							HostType: "my_host_type",
							Size:     123,
						}.Build(),
						"your_node_set": publicv1.ClusterNodeSet_builder{
							HostType: "your_host_type",
							Size:     456,
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
						"new_node_set": privatev1.ClusterNodeSet_builder{
							HostType: "new_host_type",
							Size:     789,
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"existing_node_set": publicv1.ClusterNodeSet_builder{
							HostType: "existing_host_type",
							Size:     456,
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"existing_node_set": publicv1.ClusterNodeSet_builder{
							HostType: "existing_host_type",
							Size:     456,
						}.Build(),
						"new_node_set": publicv1.ClusterNodeSet_builder{
							HostType: "new_host_type",
							Size:     789,
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
						"node_set": privatev1.ClusterNodeSet_builder{
							HostType: "updated_host_type",
							Size:     999,
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"node_set": publicv1.ClusterNodeSet_builder{
							HostType: "original_host_type",
							Size:     123,
						}.Build(),
					},
				}.Build(),
			}.Build(),
			publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"node_set": publicv1.ClusterNodeSet_builder{
							HostType: "updated_host_type",
							Size:     999,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		),
	)
})
