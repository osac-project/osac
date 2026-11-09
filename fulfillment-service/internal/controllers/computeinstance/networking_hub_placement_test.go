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

package computeinstance

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

type fakeComputeNetworkingHubReader struct {
	resolution controllers.NetworkingHubResolution
	err        error
}

func (f *fakeComputeNetworkingHubReader) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	return f.resolution, f.err
}

func readyComputeNetworkingHubReader(id, namespace string, client clnt.Client) controllers.NetworkingHubReader {
	return &fakeComputeNetworkingHubReader{
		resolution: controllers.NetworkingHubResolution{
			NetworkingHub: controllers.NetworkingHub{ID: id, Namespace: namespace, Client: client},
			HubID:         id,
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		},
	}
}

func newComputeNetworkingHubKubeClient() clnt.Client {
	scheme := runtime.NewScheme()
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

var _ = Describe("ComputeInstance networking Hub placement", func() {
	const (
		canonicalHubID = "networking-hub"
		hubNamespace   = "networking-hub-ns"
	)

	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	newReader := func(err error) *fakeComputeNetworkingHubReader {
		return &fakeComputeNetworkingHubReader{
			resolution: controllers.NetworkingHubResolution{
				NetworkingHub: controllers.NetworkingHub{
					ID:        canonicalHubID,
					Namespace: hubNamespace,
					Client:    newComputeNetworkingHubKubeClient(),
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
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "compute-instance-1",
				Metadata: privatev1.Metadata_builder{
					Tenant:     "tenant-1",
					Finalizers: []string{finalizers.Controller},
				}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
							Subnet: &privatev1.SubnetLocalReference{Id: "subnet-1"},
						}.Build(),
					},
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{Hub: assignedHub}.Build(),
			}.Build(),
		}
	}

	It("uses the canonical networking Hub for network-attached instances", func() {
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
		Expect(t.computeInstance.GetStatus().GetHub()).To(Equal("legacy-hub"))
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
		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: request.GetObject()}, nil
			}).Times(1)
		t.r.computeInstancesClient = computeInstancesClient
		t.r.maskCalculator = masks.NewCalculator().Build()

		err := t.r.run(context.Background(), t.computeInstance)
		Expect(errors.Is(err, controllers.ErrNoNetworkClass)).To(BeTrue())
		Expect(t.computeInstance.GetStatus().GetHub()).To(BeEmpty())
		Expect(t.computeInstance.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		Expect(t.computeInstance.GetStatus().GetConditions()).To(ContainElement(WithTransform(
			func(condition *privatev1.ComputeInstanceCondition) string { return condition.GetReason() },
			Equal("ResourcesUnavailable"),
		)))

		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(reader.resolution.Client.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})

	It("persists a conflicting assignment as failed and does not create a CR", func() {
		reader := newReader(nil)
		t := newNetworkedTask(reader, "legacy-hub")
		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: request.GetObject()}, nil
			}).Times(1)
		t.r.computeInstancesClient = computeInstancesClient
		t.r.maskCalculator = masks.NewCalculator().Build()

		Expect(t.r.run(context.Background(), t.computeInstance)).To(Succeed())
		Expect(t.computeInstance.GetStatus().GetHub()).To(Equal("legacy-hub"))
		Expect(t.computeInstance.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))

		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(reader.resolution.Client.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})
})
