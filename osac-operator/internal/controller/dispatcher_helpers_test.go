/*
Copyright 2026.

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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("resolveDispatchPlan", func() {
	var (
		ctx                 context.Context
		fakeDiscoveryClient client.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeDiscoveryClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "osac", "netris"),
			newK8sManagerConfigMap("km-cudn", "osac", "cudn_net", "ipv4"),
		).Build()
	})

	It("returns a nil plan and no error when the resolver is nil", func() {
		plan, err := resolveDispatchPlan(ctx, nil, "Subnet", "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).To(BeNil())
	})

	It("returns a nil plan and no error when networkClassID is empty", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(
			newListingNetworkClassClient(nil, &[]*privatev1.NetworkClass{}),
		), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).To(BeNil())
	})

	It("returns a nil plan and no error when the NetworkClass has no fabricManager set", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-unset"}}, &[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "nc-unset")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).To(BeNil())
	})

	It("resolves a fabric-only plan for a fabric-only kind", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-vnet", FabricManager: ptr.To("netris")}}, &[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "VirtualNetwork", "nc-vnet")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).NotTo(BeNil())
		Expect(plan.FabricTarget()).NotTo(BeNil())
		Expect(plan.FabricTarget().Manager.Name).To(Equal("netris"))
		Expect(plan.K8sTarget()).To(BeNil())
	})

	It("resolves a plan with both fabric and k8s targets for Subnet when the NetworkClass has a k8sManager", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		k8sManagerName := "cudn_net"
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-dual", FabricManager: ptr.To("netris"), K8SManager: &k8sManagerName}},
			&[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "nc-dual")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).NotTo(BeNil())
		Expect(plan.FabricTarget()).NotTo(BeNil())
		Expect(plan.FabricTarget().Manager.Name).To(Equal("netris"))
		Expect(plan.K8sTarget()).NotTo(BeNil())
		Expect(plan.K8sTarget().Manager.Name).To(Equal("cudn_net"))
	})

	It("resolves a fabric-only plan for Subnet when the NetworkClass has no k8sManager set", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-fabric-only", FabricManager: ptr.To("netris")}}, &[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "nc-fabric-only")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan).NotTo(BeNil())
		Expect(plan.FabricTarget()).NotTo(BeNil())
		Expect(plan.K8sTarget()).To(BeNil())
	})

	It("returns a reconcile error when the NetworkClass references an unregistered fabric manager", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-broken", FabricManager: ptr.To("does-not-exist")}}, &[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "nc-broken")
		Expect(err).To(HaveOccurred())
		Expect(plan).To(BeNil())
	})

	It("returns a reconcile error when the NetworkClass references an unregistered k8s manager", func() {
		disc, err := networkmanager.NewDiscovery(fakeDiscoveryClient, "osac")
		Expect(err).NotTo(HaveOccurred())
		unregisteredK8sManager := "does-not-exist"
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-broken-k8s", FabricManager: ptr.To("netris"), K8SManager: &unregisteredK8sManager}},
			&[]*privatev1.NetworkClass{},
		)), disc)

		plan, err := resolveDispatchPlan(ctx, resolver, "Subnet", "nc-broken-k8s")
		Expect(err).To(HaveOccurred())
		Expect(plan).To(BeNil())
	})
})

var _ = Describe("dispatchTargetProvider", func() {
	var (
		ctx      context.Context
		resource *osacv1alpha1.Subnet
	)

	BeforeEach(func() {
		ctx = context.Background()
		resource = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dispatch-target-subnet",
				Namespace: "default",
				Annotations: map[string]string{
					osacImplementationStrategyAnnotation: "original-value",
				},
			},
		}
	})

	It("overrides the implementation-strategy annotation on the resource seen by TriggerProvision, without mutating the caller's original", func() {
		var seenAnnotation string
		mock := &mockSubnetProvider{
			triggerProvisionFunc: func(_ context.Context, r client.Object) (*provisioning.ProvisionResult, error) {
				seenAnnotation = r.GetAnnotations()[osacImplementationStrategyAnnotation]
				return &provisioning.ProvisionResult{JobID: "job-1"}, nil
			},
		}
		provider := newDispatchTargetProvider(mock, "cudn_net")

		_, err := provider.TriggerProvision(ctx, resource)
		Expect(err).NotTo(HaveOccurred())
		Expect(seenAnnotation).To(Equal("cudn_net"))
		Expect(resource.Annotations[osacImplementationStrategyAnnotation]).To(Equal("original-value"))
	})

	It("overrides the implementation-strategy annotation on the resource seen by TriggerDeprovision, without mutating the caller's original", func() {
		var seenAnnotation string
		mock := &mockSubnetProvider{
			triggerDeprovisionFunc: func(_ context.Context, r client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				seenAnnotation = r.GetAnnotations()[osacImplementationStrategyAnnotation]
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "job-1"}, nil
			},
		}
		provider := newDispatchTargetProvider(mock, "netris")

		_, err := provider.TriggerDeprovision(ctx, resource, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(seenAnnotation).To(Equal("netris"))
		Expect(resource.Annotations[osacImplementationStrategyAnnotation]).To(Equal("original-value"))
	})

	It("sets the annotation when overriding a resource with no existing annotations", func() {
		bare := &osacv1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "bare-subnet", Namespace: "default"}}
		var seenAnnotation string
		mock := &mockSubnetProvider{
			triggerProvisionFunc: func(_ context.Context, r client.Object) (*provisioning.ProvisionResult, error) {
				seenAnnotation = r.GetAnnotations()[osacImplementationStrategyAnnotation]
				return &provisioning.ProvisionResult{JobID: "job-1"}, nil
			},
		}
		provider := newDispatchTargetProvider(mock, "cudn_net")

		_, err := provider.TriggerProvision(ctx, bare)
		Expect(err).NotTo(HaveOccurred())
		Expect(seenAnnotation).To(Equal("cudn_net"))
		Expect(bare.Annotations).To(BeEmpty())
	})

	It("delegates GetProvisionStatus with the exact same resource passed in, unmodified", func() {
		var seenResource client.Object
		mock := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, r client.Object, _ string) (provisioning.ProvisionStatus, error) {
				seenResource = r
				return provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newDispatchTargetProvider(mock, "cudn_net")

		_, err := provider.GetProvisionStatus(ctx, resource, "job-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(seenResource).To(BeIdenticalTo(resource))
	})

	It("delegates GetDeprovisionStatus with the exact same resource passed in, unmodified", func() {
		var seenResource client.Object
		mock := &mockSubnetProvider{
			getDeprovisionStatusFunc: func(_ context.Context, r client.Object, _ string) (provisioning.ProvisionStatus, error) {
				seenResource = r
				return provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newDispatchTargetProvider(mock, "cudn_net")

		_, err := provider.GetDeprovisionStatus(ctx, resource, "job-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(seenResource).To(BeIdenticalTo(resource))
	})

	It("delegates Name to the base provider", func() {
		mock := &mockSubnetProvider{}
		provider := newDispatchTargetProvider(mock, "cudn_net")
		Expect(provider.Name()).To(Equal(mock.Name()))
	})
})
