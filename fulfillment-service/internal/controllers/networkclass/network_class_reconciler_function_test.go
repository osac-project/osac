/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use a copy of this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkClassHubResolver struct {
	calls  int
	result controllers.NetworkingHubResolution
	err    error
}

func (f *fakeNetworkClassHubResolver) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	f.calls++
	if f.result.State == privatev1.NetworkClassState_NETWORK_CLASS_STATE_UNSPECIFIED {
		f.result = controllers.NetworkingHubResolution{
			NetworkingHub: controllers.NetworkingHub{ID: "hub-a"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}
	}
	return f.result, f.err
}

type fakeNetworkClassStatusClient struct {
	updates []*privatev1.NetworkClassesUpdateRequest
	err     error
}

func (f *fakeNetworkClassStatusClient) Update(
	_ context.Context,
	request *privatev1.NetworkClassesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.NetworkClassesUpdateResponse, error) {
	f.updates = append(f.updates, proto.Clone(request).(*privatev1.NetworkClassesUpdateRequest))
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.NetworkClassesUpdateResponse_builder{Object: request.GetObject()}.Build(), nil
}

var _ = Describe("NetworkClass reconciler", func() {
	It("delegates canonical Hub resolution to the NetworkClass reconciler", func() {
		resolver := &fakeNetworkClassHubResolver{}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id:       "nc-a",
			Metadata: withFinalizer(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(resolver.calls).To(Equal(1))
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(Equal("hub-a"))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING),
			"the operator reports READY from the manager registrations")
		Expect(client.updates[0].GetUpdateMask().GetPaths()).To(ConsistOf(
			"status.hub", "status.state", "status.message"))
		Expect(client.updates[0].GetLock()).To(BeTrue())
	})

	It("does not write status when the resolution is already persisted", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				NetworkingHub: controllers.NetworkingHub{ID: "hub-a"},
				HubID:         "hub-a",
				State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			},
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id:       "nc-a",
			Metadata: withFinalizer(),
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "hub-a",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(client.updates).To(BeEmpty())
	})

	It("keeps a READY NetworkClass READY when the hub resolution fails", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "no networking hub is available",
			},
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id:       "nc-a",
			Metadata: withFinalizer(),
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "hub-a",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(client.updates).To(HaveLen(1))
		status := client.updates[0].GetObject().GetStatus()
		Expect(status.GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY),
			"the operator resolves a READY NetworkClass only once")
		Expect(status.GetMessage()).To(Equal("no networking hub is available"))
	})

	It("adds the controller finalizer before anything else", func() {
		resolver := &fakeNetworkClassHubResolver{}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		Expect(reconcile(context.Background(), privatev1.NetworkClass_builder{Id: "nc-a"}.Build())).To(Succeed())
		Expect(resolver.calls).To(BeZero())
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("metadata.finalizers"))
		Expect(client.updates[0].GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("creates the NetworkClass CR with the address plan in sixteenths", func() {
		kube := newKubeClient()
		resolver := &fakeNetworkClassHubResolver{result: readyResolution(kube)}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		Expect(reconcile(context.Background(), fullNetworkClass())).To(Succeed())

		list := &osacv1alpha1.NetworkClassList{}
		Expect(kube.List(context.Background(), list, clnt.InNamespace("osac-networking"))).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		object := list.Items[0]
		Expect(object.Labels).To(HaveKeyWithValue(labels.NetworkClassUuid, "nc-a"))
		Expect(object.Spec.FabricManager).To(Equal("netris"))
		Expect(object.Spec.K8sManager).To(Equal("cudn"))
		Expect(object.Spec.AddressPlan).To(Equal(osacv1alpha1.SubnetAddressPlan{Overlay: 12, Fabric: 2, LBVIP: 2}))
		Expect(object.Spec.Defaults.VirtualNetworkIPv4CIDR).To(Equal("10.0.0.0/16"))
		Expect(object.Spec.Defaults.SubnetIPv4CIDR).To(Equal("10.0.0.0/24"))
		Expect(object.Spec.Defaults.IngressRules).To(HaveLen(1))
		Expect(object.Spec.Defaults.IngressRules[0].SourceCIDR).To(Equal("10.0.0.0/16"))
		Expect(object.Spec.EastWestCapabilities).ToNot(BeNil())
		Expect(object.Spec.EastWestCapabilities.SupportsEastWestEthernet).To(BeTrue())
	})

	It("updates only the address plan of an existing CR", func() {
		kube := newKubeClient()
		resolver := &fakeNetworkClassHubResolver{result: readyResolution(kube)}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())
		networkClass := fullNetworkClass()
		Expect(reconcile(context.Background(), networkClass)).To(Succeed())

		networkClass.GetSpec().GetAddressPlan().SetOverlay(0.5)
		networkClass.GetSpec().GetAddressPlan().SetFabric(0.25)
		networkClass.GetSpec().GetAddressPlan().SetLbVip(0.25)
		Expect(reconcile(context.Background(), networkClass)).To(Succeed())

		list := &osacv1alpha1.NetworkClassList{}
		Expect(kube.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Spec.AddressPlan).To(Equal(osacv1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4}))
	})

	It("deletes the CR and waits for it before removing the finalizer", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		kube := newKubeClient()
		Expect(kube.Create(context.Background(), &osacv1alpha1.NetworkClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "osac-networking",
				Name:      "networkclass-a",
				Labels:    map[string]string{labels.NetworkClassUuid: "nc-a"},
			},
		})).To(Succeed())
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(gomock.Any(), "hub-a").Return(&controllers.HubEntry{
			Namespace: "osac-networking",
			Client:    kube,
		}, nil).AnyTimes()
		client := &fakeNetworkClassStatusClient{}
		r := &function{resolver: &fakeNetworkClassHubResolver{}, client: client, hubCache: hubCache}
		networkClass := fullNetworkClass()
		networkClass.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		networkClass.SetStatus(privatev1.NetworkClassStatus_builder{Hub: "hub-a"}.Build())

		err := r.run(context.Background(), networkClass)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(client.updates).To(BeEmpty())

		err = r.run(context.Background(), networkClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("does not resolve a deleted NetworkClass", func() {
		resolver := &fakeNetworkClassHubResolver{}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Metadata: privatev1.Metadata_builder{
				DeletionTimestamp: timestamppb.Now(),
				Finalizers:        []string{finalizers.Controller},
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(resolver.calls).To(Equal(0))
	})

	It("clears a stale Hub when discovery has no candidates", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "expected exactly one active networking hub, found none",
			},
			err: controllers.ErrNoNetworkingHubs,
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id:       "nc-a",
			Metadata: withFinalizer(),
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "previous-hub",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(MatchError(controllers.ErrNoNetworkingHubs))
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY), "READY is never downgraded")
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(BeEmpty())
		Expect(client.updates[0].GetObject().GetStatus().GetMessage()).To(
			Equal("expected exactly one active networking hub, found none"))
	})

	It("preserves a persisted Hub and marks the NetworkClass failed when it is not registered", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				HubID:   "missing",
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED,
				Message: "canonical networking hub \"missing\" is not registered",
			},
			err: errors.Join(controllers.ErrCanonicalHubNotFound, errors.New("missing")),
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id:       "nc-a",
			Metadata: withFinalizer(),
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "missing",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(HaveOccurred())
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(Equal("missing"))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
	})

	It("returns the status update error without hiding the resolution error", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "expected exactly one active networking hub, found none",
			},
			err: controllers.ErrNoNetworkingHubs,
		}
		client := &fakeNetworkClassStatusClient{err: errors.New("optimistic lock failed")}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{Id: "nc-a", Metadata: withFinalizer()}.Build()

		err = reconcile(context.Background(), networkClass)
		Expect(errors.Is(err, controllers.ErrNoNetworkingHubs)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("failed to update network class status"))
	})
})

func withFinalizer() *privatev1.Metadata {
	return privatev1.Metadata_builder{Finalizers: []string{finalizers.Controller}}.Build()
}

func newKubeClient() clnt.Client {
	scheme := runtime.NewScheme()
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func readyResolution(kube clnt.Client) controllers.NetworkingHubResolution {
	return controllers.NetworkingHubResolution{
		NetworkingHub: controllers.NetworkingHub{ID: "hub-a", Namespace: "osac-networking", Client: kube},
		HubID:         "hub-a",
		State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
	}
}

func fullNetworkClass() *privatev1.NetworkClass {
	return privatev1.NetworkClass_builder{
		Id:            "nc-a",
		Metadata:      withFinalizer(),
		FabricManager: "netris",
		K8SManager:    proto.String("cudn"),
		Spec: privatev1.NetworkClassSpec_builder{
			Defaults: privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.0.0.0/16",
				SubnetIpv4Cidr:         "10.0.0.0/24",
				IngressRules: []*privatev1.SecurityRule{
					privatev1.SecurityRule_builder{
						Protocol: privatev1.Protocol_PROTOCOL_ALL,
						Ipv4Cidr: proto.String("10.0.0.0/16"),
					}.Build(),
				},
			}.Build(),
			EastWestCapabilities: privatev1.NetworkClassEastWestCapabilities_builder{
				SupportsEastWestEthernet: true,
			}.Build(),
			AddressPlan: privatev1.AddressPlan_builder{Overlay: 0.75, Fabric: 0.125, LbVip: 0.125}.Build(),
		}.Build(),
	}.Build()
}
