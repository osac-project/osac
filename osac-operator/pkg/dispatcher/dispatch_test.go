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

package dispatcher_test

import (
	"context"
	"fmt"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"google.golang.org/grpc"
)

var _ = Describe("DispatchTable", func() {
	It("returns config for VirtualNetwork", func() {
		cfg := dispatcher.LookupDispatchConfig("VirtualNetwork")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for Subnet with both roles", func() {
		cfg := dispatcher.LookupDispatchConfig("Subnet")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric, dispatcher.ManagerRoleK8s))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for SecurityGroup", func() {
		cfg := dispatcher.LookupDispatchConfig("SecurityGroup")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for ExternalIP", func() {
		cfg := dispatcher.LookupDispatchConfig("ExternalIP")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for ExternalIPPool", func() {
		cfg := dispatcher.LookupDispatchConfig("ExternalIPPool")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for ExternalIPAttachment", func() {
		cfg := dispatcher.LookupDispatchConfig("ExternalIPAttachment")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeTrue())
	})

	It("returns config for NATGateway with no k8s fallback", func() {
		cfg := dispatcher.LookupDispatchConfig("NATGateway")
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Roles).To(ConsistOf(dispatcher.ManagerRoleFabric))
		Expect(cfg.K8sFallback).To(BeFalse())
	})

	It("returns nil for unknown kind", func() {
		cfg := dispatcher.LookupDispatchConfig("UnknownKind")
		Expect(cfg).To(BeNil())
	})

	It("lists all known kinds", func() {
		kinds := dispatcher.KnownKinds()
		sort.Strings(kinds)
		Expect(kinds).To(ContainElements(
			"ExternalIP",
			"ExternalIPAttachment",
			"ExternalIPPool",
			"NATGateway",
			"SecurityGroup",
			"Subnet",
			"VirtualNetwork",
		))
	})
})

var _ = Describe("DispatchPlan", func() {
	It("HasRole returns true for present role", func() {
		plan := &dispatcher.DispatchPlan{
			Targets: []dispatcher.DispatchTarget{
				{Role: dispatcher.ManagerRoleFabric},
				{Role: dispatcher.ManagerRoleK8s},
			},
		}
		Expect(plan.HasRole(dispatcher.ManagerRoleFabric)).To(BeTrue())
		Expect(plan.HasRole(dispatcher.ManagerRoleK8s)).To(BeTrue())
	})

	It("HasRole returns false for absent role", func() {
		plan := &dispatcher.DispatchPlan{
			Targets: []dispatcher.DispatchTarget{
				{Role: dispatcher.ManagerRoleFabric},
			},
		}
		Expect(plan.HasRole(dispatcher.ManagerRoleK8s)).To(BeFalse())
	})

	It("FabricTarget returns the fabric target", func() {
		plan := &dispatcher.DispatchPlan{
			Targets: []dispatcher.DispatchTarget{
				{Role: dispatcher.ManagerRoleFabric, Manager: networkmanager.Manager{Name: "netris"}},
			},
		}
		t := plan.FabricTarget()
		Expect(t).NotTo(BeNil())
		Expect(t.Manager.Name).To(Equal("netris"))
	})

	It("K8sTarget returns nil when no k8s target", func() {
		plan := &dispatcher.DispatchPlan{
			Targets: []dispatcher.DispatchTarget{
				{Role: dispatcher.ManagerRoleFabric},
			},
		}
		Expect(plan.K8sTarget()).To(BeNil())
	})
})

var _ = Describe("Dispatcher", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	newStubWithManagers := func(fabricName string, k8sName *string) *stubNetworkClassesClient {
		return &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-test",
						FabricManager: fabricName,
						K8SManager:    k8sName,
					},
				}, nil
			},
		}
	}

	It("dispatches VirtualNetwork to fabric only", func() {
		stub := newStubWithManagers("netris", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "VirtualNetwork", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.Targets[0].Role).To(Equal(dispatcher.ManagerRoleFabric))
		Expect(plan.Targets[0].Manager.Name).To(Equal("netris"))
	})

	It("dispatches Subnet to fabric + k8s when both configured", func() {
		k8sName := "cudn_localnet"
		stub := newStubWithManagers("neutron", &k8sName)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-neutron", "neutron", "ipv4,ipv6,dualStack"),
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "Subnet", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(2))
		Expect(plan.HasRole(dispatcher.ManagerRoleFabric)).To(BeTrue())
		Expect(plan.HasRole(dispatcher.ManagerRoleK8s)).To(BeTrue())
		Expect(plan.FabricTarget().Manager.Name).To(Equal("neutron"))
		Expect(plan.K8sTarget().Manager.Name).To(Equal("cudn_localnet"))
	})

	It("dispatches Subnet to fabric only when k8sManager is nil", func() {
		stub := newStubWithManagers("netris", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "Subnet", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.HasRole(dispatcher.ManagerRoleFabric)).To(BeTrue())
		Expect(plan.HasRole(dispatcher.ManagerRoleK8s)).To(BeFalse())
	})

	It("dispatches SecurityGroup to fabric only", func() {
		stub := newStubWithManagers("netris", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "SecurityGroup", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.Targets[0].Role).To(Equal(dispatcher.ManagerRoleFabric))
	})

	It("dispatches NATGateway to fabric only", func() {
		stub := newStubWithManagers("netris", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "NATGateway", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.Targets[0].Role).To(Equal(dispatcher.ManagerRoleFabric))
	})

	It("returns error for unknown resource kind", func() {
		stub := newStubWithManagers("netris", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		_, err = d.Dispatch(ctx, "UnknownKind", "nc-test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no dispatch configuration"))
		Expect(err.Error()).To(ContainSubstring("UnknownKind"))
	})

	It("dispatches VirtualNetwork to k8s manager when no fabric manager is set (fallback)", func() {
		k8sName := "cudn_localnet"
		stub := newStubWithManagers("", &k8sName)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "VirtualNetwork", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.Targets[0].Role).To(Equal(dispatcher.ManagerRoleK8s))
		Expect(plan.Targets[0].Manager.Name).To(Equal("cudn_localnet"))
	})

	DescribeTable("dispatches fallback-eligible kinds to the k8s manager when no fabric manager is set",
		func(kind string) {
			k8sName := "cudn_localnet"
			stub := newStubWithManagers("", &k8sName)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
			).Build()

			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

			plan, err := d.Dispatch(ctx, kind, "nc-test")
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.HasRole(dispatcher.ManagerRoleK8s)).To(BeTrue())
			Expect(plan.K8sTarget().Manager.Name).To(Equal("cudn_localnet"))
		},
		Entry("SecurityGroup", "SecurityGroup"),
		Entry("ExternalIP", "ExternalIP"),
		Entry("ExternalIPPool", "ExternalIPPool"),
		Entry("ExternalIPAttachment", "ExternalIPAttachment"),
	)

	It("dispatches Subnet to exactly one k8s target when no fabric manager is set (dedupe)", func() {
		k8sName := "cudn_localnet"
		stub := newStubWithManagers("", &k8sName)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		plan, err := d.Dispatch(ctx, "Subnet", "nc-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Targets).To(HaveLen(1))
		Expect(plan.Targets[0].Role).To(Equal(dispatcher.ManagerRoleK8s))
		Expect(plan.Targets[0].Manager.Name).To(Equal("cudn_localnet"))
	})

	It("returns an error dispatching NATGateway when no fabric manager is set (no fallback)", func() {
		k8sName := "cudn_localnet"
		stub := newStubWithManagers("", &k8sName)
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		_, err = d.Dispatch(ctx, "NATGateway", "nc-test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no manager available"))
	})

	It("returns an error dispatching a fallback-eligible kind when neither manager is set", func() {
		stub := newStubWithManagers("", nil)
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		_, err = d.Dispatch(ctx, "VirtualNetwork", "nc-test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("neither fabricManager nor k8sManager is set"))
	})

	It("propagates resolver errors", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return nil, fmt.Errorf("connection refused")
			},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())

		d := dispatcher.NewDispatcher(dispatcher.NewResolver(stub, disc))

		_, err = d.Dispatch(ctx, "VirtualNetwork", "nc-test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("resolving managers"))
	})
})
