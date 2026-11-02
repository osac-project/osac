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
	"encoding/json"
	"errors"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("maybeMarkNetworkRepair", func() {
	var (
		ctx           context.Context
		reconciler    *BareMetalInstanceReconciler
		mockInvClient *mockInventoryClient
		bmi           *v1alpha1.BareMetalInstance
		fabricDomain  *opv1alpha1.FabricDomain
		log           logr.Logger
	)

	BeforeEach(func() {
		ctx = context.Background()
		log = logr.Discard()
		mockInvClient = &mockInventoryClient{}
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: "repair-mark-bmi",
				Labels: map[string]string{
					BareMetalInstanceUUIDLabel: "bmi-api-id",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID:   "metal3-system/host-a",
				ExternalHostName: "host-a",
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{
					SubnetRef: "subnet-api-id",
					Interface: "eno1",
				}},
			},
		}

		fabricDomain = &opv1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "fd-repair-",
				Namespace:    testNetworkingNS,
				Labels: map[string]string{
					FabricDomainUUIDLabel: "fd-api-id",
				},
			},
			Spec: opv1alpha1.FabricDomainSpec{
				Type:           opv1alpha1.FabricDomainTypeEthernetEW,
				Servers:        []string{"host-a", "host-b"},
				VirtualNetwork: "vnet-1",
			},
			Status: opv1alpha1.FabricDomainStatus{Jobs: []opv1alpha1.NetworkJobStatus{{
				Task: "create_fabric_domain", Manager: opv1alpha1.NetworkJobManagerFabric,
				Attempt: 1, State: opv1alpha1.NetworkJobStateRunning, JobID: "provider-job",
			}}},
		}

		reconciler = &BareMetalInstanceReconciler{
			Client:              k8sClient,
			Scheme:              k8sClient.Scheme(),
			InventoryClient:     mockInvClient,
			NetworkingNamespace: testNetworkingNS,
		}
	})

	AfterEach(func() {
		if fabricDomain != nil && fabricDomain.Name != "" {
			_ = k8sClient.Delete(ctx, fabricDomain)
		}
	})

	It("writes repair details naming the FabricDomain API ID", func() {
		createStartedFabricDomain(ctx, fabricDomain)

		Expect(reconciler.maybeMarkNetworkRepair(ctx, log, bmi)).To(Succeed())
		Expect(mockInvClient.markNetworkRepairCalled).To(Equal(1))

		var details networkRepairDetails
		Expect(json.Unmarshal([]byte(mockInvClient.markNetworkRepairLastDetails), &details)).To(Succeed())
		Expect(details.BareMetalInstance).To(Equal("bmi-api-id"))
		Expect(details.Subnet).To(Equal("subnet-api-id"))
		Expect(details.Interface).To(Equal("eno1"))
		Expect(details.FabricDomains).To(Equal([]string{"fd-api-id"}))
	})

	It("skips when no FabricDomain lists the host", func() {
		fabricDomain.Spec.Servers = []string{"other-host"}
		createStartedFabricDomain(ctx, fabricDomain)

		Expect(reconciler.maybeMarkNetworkRepair(ctx, log, bmi)).To(Succeed())
		Expect(mockInvClient.markNetworkRepairCalled).To(BeZero())
	})

	It("fails closed when the inventory backend cannot write a repair mark", func() {
		createStartedFabricDomain(ctx, fabricDomain)
		mockInvClient.markNetworkRepairFunc = func(_ context.Context, _, _ string) error {
			return inventory.ErrNetworkRepairMarkUnsupported
		}

		err := reconciler.maybeMarkNetworkRepair(ctx, log, bmi)
		Expect(err).To(MatchError(inventory.ErrNetworkRepairMarkUnsupported))
		Expect(mockInvClient.markNetworkRepairCalled).To(Equal(1))
	})

	It("returns inventory errors so release can retry", func() {
		createStartedFabricDomain(ctx, fabricDomain)
		mockInvClient.markNetworkRepairFunc = func(_ context.Context, _, _ string) error {
			return errors.New("bmh update conflict")
		}

		err := reconciler.maybeMarkNetworkRepair(ctx, log, bmi)
		Expect(err).To(MatchError(ContainSubstring("bmh update conflict")))
	})

	It("lists FabricDomains through the uncached API reader", func() {
		cachedClient := &readTrackingClient{Client: k8sClient}
		apiReader := &readTrackingReader{Reader: k8sClient}
		reconciler.Client = cachedClient
		reconciler.APIReader = apiReader

		_, err := reconciler.fabricDomainsListingHost(ctx, "host-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(apiReader.listCalls).To(Equal(1))
		Expect(cachedClient.listCalls).To(BeZero())
	})

	It("skips when provider networking is disabled", func() {
		reconciler.NetworkingNamespace = ""
		Expect(k8sClient.Create(ctx, fabricDomain)).To(Succeed())

		Expect(reconciler.maybeMarkNetworkRepair(ctx, log, bmi)).To(Succeed())
		Expect(mockInvClient.markNetworkRepairCalled).To(BeZero())
	})

	It("skips when the matching FabricDomain has not started creation", func() {
		fabricDomain.Status.Jobs = nil
		Expect(k8sClient.Create(ctx, fabricDomain)).To(Succeed())

		Expect(reconciler.maybeMarkNetworkRepair(ctx, log, bmi)).To(Succeed())
		Expect(mockInvClient.markNetworkRepairCalled).To(BeZero())
	})
})

var _ = Describe("handleDeletion repair mark ordering", func() {
	var (
		ctx           context.Context
		reconciler    *BareMetalInstanceReconciler
		mockK8sClient *mockClient
		mockInvClient *mockInventoryClient
		bmi           *v1alpha1.BareMetalInstance
		fabricDomain  *opv1alpha1.FabricDomain
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockK8sClient = &mockClient{Client: k8sClient}
		mockInvClient = &mockInventoryClient{}
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "repair-order-bmi",
				Namespace:         "default",
				UID:               "bmi-repair-uid",
				DeletionTimestamp: &metav1.Time{Time: time.Now()},
				Labels: map[string]string{
					BareMetalInstanceUUIDLabel: "bmi-api-id",
				},
				Finalizers: []string{BareMetalInstanceInventoryFinalizer},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID:   "metal3-system/host-a",
				ExternalHostName: "host-a",
			},
		}
		fabricDomain = &opv1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "fd-repair-order-",
				Namespace:    testNetworkingNS,
				Labels:       map[string]string{FabricDomainUUIDLabel: "fd-api-id"},
			},
			Spec: opv1alpha1.FabricDomainSpec{
				Type:           opv1alpha1.FabricDomainTypeEthernetEW,
				Servers:        []string{"host-a"},
				VirtualNetwork: "vnet-1",
			},
		}
		createStartedFabricDomain(ctx, fabricDomain)

		reconciler = &BareMetalInstanceReconciler{
			Client:              mockK8sClient,
			Scheme:              k8sClient.Scheme(),
			InventoryClient:     mockInvClient,
			ManagementClient:    &mockManagementClient{},
			NetworkingNamespace: testNetworkingNS,
		}
		mockK8sClient.updateFunc = func(context.Context, client.Object, ...client.UpdateOption) error {
			return nil
		}
	})

	AfterEach(func() {
		if fabricDomain != nil && fabricDomain.Name != "" {
			_ = k8sClient.Delete(ctx, fabricDomain)
		}
	})

	It("marks before UnassignHost and keeps the finalizer when mark fails", func() {
		mockInvClient.markNetworkRepairFunc = func(_ context.Context, _, _ string) error {
			return errors.New("bmh update conflict")
		}
		unassignCalled := false
		mockInvClient.unassignHostFunc = func(_ context.Context, _, _ string, _ []string) error {
			unassignCalled = true
			return nil
		}

		_, err := reconciler.handleDeletion(ctx, bmi)
		Expect(err).To(MatchError(ContainSubstring("bmh update conflict")))
		Expect(unassignCalled).To(BeFalse())
		Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceInventoryFinalizer)).To(BeTrue())
	})

	It("marks then unassigns on success", func() {
		unassignCalled := false
		mockInvClient.unassignHostFunc = func(_ context.Context, _, _ string, _ []string) error {
			Expect(mockInvClient.markNetworkRepairCalled).To(Equal(1))
			unassignCalled = true
			return nil
		}
		mockK8sClient.updateFunc = func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
			Expect(controllerutil.ContainsFinalizer(obj.(*v1alpha1.BareMetalInstance), BareMetalInstanceInventoryFinalizer)).To(BeFalse())
			return nil
		}

		result, err := reconciler.handleDeletion(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
		Expect(unassignCalled).To(BeTrue())
	})
})

var _ = Describe("fabricDomainListsServer", func() {
	It("matches ExternalHostName against Spec.Servers", func() {
		fd := &opv1alpha1.FabricDomain{
			Status: opv1alpha1.FabricDomainStatus{Jobs: []opv1alpha1.NetworkJobStatus{{
				Task: "create_fabric_domain", Manager: opv1alpha1.NetworkJobManagerFabric,
				Attempt: 1, State: opv1alpha1.NetworkJobStateRunning, JobID: "provider-job",
			}}},
			Spec: opv1alpha1.FabricDomainSpec{Servers: []string{"a", "b"}},
		}
		Expect(fabricDomainListsServer(fd, "b")).To(BeTrue())
		Expect(fabricDomainListsServer(fd, "c")).To(BeFalse())
	})
})

func createStartedFabricDomain(ctx context.Context, fd *opv1alpha1.FabricDomain) {
	Expect(k8sClient.Create(ctx, fd)).To(Succeed())
	fd.Status.Jobs = []opv1alpha1.NetworkJobStatus{{
		Task: "create_fabric_domain", Manager: opv1alpha1.NetworkJobManagerFabric,
		Attempt: 1, State: opv1alpha1.NetworkJobStateRunning, JobID: "provider-job",
	}}
	Expect(k8sClient.Status().Update(ctx, fd)).To(Succeed())
	stored := &opv1alpha1.FabricDomain{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(fd), stored)).To(Succeed())
	Expect(stored.Status.Jobs).To(HaveLen(1))
	Expect(fabricDomainCreateStarted(stored)).To(BeTrue())
}
