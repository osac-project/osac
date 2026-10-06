/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = It("tracks independent NodePool capacity when NodeSets share hardware", func() {
	requests := []v1alpha1.NodeRequest{
		{NodeSet: "compute", NumberOfNodes: 2, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"}},
		{NodeSet: "batch", NumberOfNodes: 3, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"}},
	}
	compute, batch := readyClusterOrderNodePool("compute", 2), readyClusterOrderNodePool("batch", 3)
	compute.Labels[agentInstanceTypeLabel], batch.Labels[agentInstanceTypeLabel] = "bm.large", "bm.large"
	pools := []hypershiftv1beta1.NodePool{compute, batch}
	Expect(nodePoolsMatchRequests(requests, pools)).To(BeTrue(), "same hardware must not collapse distinct NodeSets")
	pools[0].Status.Replicas = 3
	Expect(nodePoolsMatchRequests(requests, pools)).To(BeFalse(), "batch capacity must not mask compute's incorrect capacity")
	delete(compute.Labels, agentNodeSetLabel)
	Expect(nodePoolsMatchRequests(requests, []hypershiftv1beta1.NodePool{compute, batch})).To(BeFalse(), "instance type must not be used as a fallback NodeSet marker")
})

var _ = It("synchronizes feedback by NodeSet rather than the first matching instance type", func() {
	remote := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
		"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "bm.large"}.Build()}.Build(),
		"batch":   privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "bm.large"}.Build()}.Build(),
	}}.Build(), Status: privatev1.ClusterStatus_builder{}.Build()}.Build()
	co := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{NodeRequests: []v1alpha1.NodeRequestStatus{
		{NodeSet: "compute", NumberOfNodes: 1, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"}},
		{NodeSet: "batch", NumberOfNodes: 3, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"}},
	}}}
	syncClusterOrderNodeRequests(context.Background(), co, remote)
	sets := remote.GetStatus().GetNodeSets()
	Expect(sets["compute"].GetSize()).To(Equal(int32(1)), "feedback mixed independent groups: %v", sets)
	Expect(sets["batch"].GetSize()).To(Equal(int32(3)), "feedback mixed independent groups: %v", sets)
})
