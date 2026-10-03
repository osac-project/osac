/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeClusterNetworkingHubReader struct {
	resolution controllers.NetworkingHubResolution
	err        error
}

func (f *fakeClusterNetworkingHubReader) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	return f.resolution, f.err
}

func newClusterNetworkingHubKubeClient() clnt.Client {
	scheme := runtime.NewScheme()
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

var _ = Describe("Cluster networking Hub placement", func() {
	const (
		canonicalHubID = "networking-hub"
		hubNamespace   = "networking-hub-ns"
	)
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	newReader := func(err error) *fakeClusterNetworkingHubReader {
		return &fakeClusterNetworkingHubReader{
			resolution: controllers.NetworkingHubResolution{
				NetworkingHub: controllers.NetworkingHub{
					ID:        canonicalHubID,
					Namespace: hubNamespace,
					Client:    newClusterNetworkingHubKubeClient(),
				},
				HubID: canonicalHubID,
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			},
			err: err,
		}
	}

	newNetworkedTask := func(reader controllers.NetworkingHubReader, assignedHub string) *task {
		return &task{
			r: &function{
				logger:              logger,
				networkingHubReader: reader,
			},
			cluster: privatev1.Cluster_builder{
				Id: "cluster-1",
				Metadata: privatev1.Metadata_builder{
					Tenant:     "tenant-1",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					NetworkAttachment: &privatev1.ClusterNetworkAttachment{
						Subnet: &privatev1.SubnetLocalReference{Id: "subnet-1"},
					},
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					Hub:   assignedHub,
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				}.Build(),
			}.Build(),
		}
	}

	It("uses the canonical networking Hub for network-attached clusters", func() {
		t := newNetworkedTask(newReader(nil), "")

		Expect(t.selectHub(context.Background())).To(Succeed())
		Expect(t.hubId).To(Equal(canonicalHubID))
		Expect(t.hubNamespace).To(Equal(hubNamespace))
		Expect(t.hubClient).ToNot(BeNil())
	})

	It("rejects an existing assignment that conflicts with the canonical networking Hub", func() {
		t := newNetworkedTask(newReader(nil), "legacy-hub")

		err := t.selectHub(context.Background())
		Expect(errors.Is(err, controllers.ErrResourceHubConflict)).To(BeTrue())
		Expect(t.cluster.GetStatus().GetHub()).To(Equal("legacy-hub"))
		Expect(t.hubClient).To(BeNil())
	})

	It("does not fall back to random placement when canonical Hub resolution is unavailable", func() {
		t := newNetworkedTask(newReader(controllers.ErrNoNetworkClass), "")

		err := t.selectHub(context.Background())
		Expect(errors.Is(err, controllers.ErrNoNetworkClass)).To(BeTrue())
		Expect(t.hubId).To(BeEmpty())
		Expect(t.hubClient).To(BeNil())
	})

	It("persists retryable networking unavailability without assigning a Hub or creating a CR", func() {
		reader := newReader(controllers.ErrNoNetworkClass)
		t := newNetworkedTask(reader, "")
		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ClustersUpdateRequest, _ ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				return &privatev1.ClustersUpdateResponse{Object: request.GetObject()}, nil
			}).Times(1)
		t.r.clustersClient = clustersClient
		t.r.maskCalculator = masks.NewCalculator().Build()

		err := t.r.run(context.Background(), t.cluster)
		Expect(errors.Is(err, controllers.ErrNoNetworkClass)).To(BeTrue())
		Expect(t.cluster.GetStatus().GetHub()).To(BeEmpty())
		Expect(t.cluster.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
		var progressing *privatev1.ClusterCondition
		for _, condition := range t.cluster.GetStatus().GetConditions() {
			if condition.GetType() == privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING {
				progressing = condition
			}
		}
		Expect(progressing).ToNot(BeNil())
		Expect(progressing.GetReason()).To(Equal("ResourcesUnavailable"))

		list := &osacv1alpha1.ClusterOrderList{}
		Expect(reader.resolution.Client.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})

	It("persists a conflicting assignment as failed and does not create a CR", func() {
		reader := newReader(nil)
		t := newNetworkedTask(reader, "legacy-hub")
		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ClustersUpdateRequest, _ ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				return &privatev1.ClustersUpdateResponse{Object: request.GetObject()}, nil
			}).Times(1)
		t.r.clustersClient = clustersClient
		t.r.maskCalculator = masks.NewCalculator().Build()

		Expect(t.r.run(context.Background(), t.cluster)).To(Succeed())
		Expect(t.cluster.GetStatus().GetHub()).To(Equal("legacy-hub"))
		Expect(t.cluster.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_FAILED))

		list := &osacv1alpha1.ClusterOrderList{}
		Expect(reader.resolution.Client.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})
})
