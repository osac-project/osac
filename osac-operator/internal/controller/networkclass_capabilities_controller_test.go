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
	"fmt"

	"google.golang.org/grpc"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

var _ = Describe("computeCapabilities", func() {
	fabricDualStack := networkmanager.Manager{
		Name: "netris",
		Capabilities: []networkmanager.Capability{
			networkmanager.CapabilityIPv4, networkmanager.CapabilityIPv6, networkmanager.CapabilityDualStack,
		},
		Type: networkmanager.FabricManager,
	}
	fabricIPv4Only := networkmanager.Manager{
		Name:         "netris-v4",
		Capabilities: []networkmanager.Capability{networkmanager.CapabilityIPv4},
		Type:         networkmanager.FabricManager,
	}
	k8sIPv4Only := networkmanager.Manager{
		Name:         "cudn-localnet",
		Capabilities: []networkmanager.Capability{networkmanager.CapabilityIPv4},
		Type:         networkmanager.K8sManager,
	}
	k8sIPv4AndDPU := networkmanager.Manager{
		Name:         "cudn-localnet-dpu",
		Capabilities: []networkmanager.Capability{networkmanager.CapabilityIPv4, networkmanager.CapabilityDPUSupport},
		Type:         networkmanager.K8sManager,
	}

	It("uses the fabric manager's capabilities as-is when no k8s manager is configured", func() {
		caps := computeCapabilities(&dispatcher.ResolvedManagers{FabricManager: &fabricDualStack})
		Expect(caps.GetSupportsIpv4()).To(BeTrue())
		Expect(caps.GetSupportsIpv6()).To(BeTrue())
		Expect(caps.GetSupportsDualStack()).To(BeTrue())
		Expect(caps.GetDpuSupport()).To(BeFalse())
	})

	It("intersects fabric and k8s capabilities, dropping what the k8s manager lacks", func() {
		k8s := k8sIPv4Only
		caps := computeCapabilities(&dispatcher.ResolvedManagers{FabricManager: &fabricDualStack, K8sManager: &k8s})
		Expect(caps.GetSupportsIpv4()).To(BeTrue())
		Expect(caps.GetSupportsIpv6()).To(BeFalse())
		Expect(caps.GetSupportsDualStack()).To(BeFalse())
		Expect(caps.GetDpuSupport()).To(BeFalse())
	})

	It("drops capabilities the fabric manager doesn't declare even when the k8s manager does", func() {
		k8s := k8sIPv4AndDPU
		caps := computeCapabilities(&dispatcher.ResolvedManagers{FabricManager: &fabricIPv4Only, K8sManager: &k8s})
		Expect(caps.GetSupportsIpv4()).To(BeTrue())
		Expect(caps.GetDpuSupport()).To(BeFalse())
	})
})

var _ = Describe("capabilitiesEqual", func() {
	It("treats nil as equivalent to all-false", func() {
		Expect(capabilitiesEqual(nil, &privatev1.NetworkClassCapabilities{})).To(BeTrue())
	})

	It("returns false when any field differs", func() {
		a := &privatev1.NetworkClassCapabilities{SupportsIpv4: true}
		b := &privatev1.NetworkClassCapabilities{}
		Expect(capabilitiesEqual(a, b)).To(BeFalse())
	})

	It("returns true when all fields match", func() {
		a := &privatev1.NetworkClassCapabilities{SupportsIpv4: true, SupportsDualStack: true}
		b := &privatev1.NetworkClassCapabilities{SupportsIpv4: true, SupportsDualStack: true}
		Expect(capabilitiesEqual(a, b)).To(BeTrue())
	})
})

var _ = Describe("NetworkClassCapabilitiesReconciler", func() {
	const namespace = "default"

	It("computes the intersection and updates the NetworkClass when capabilities changed", func() {
		fabricCM := newFabricManagerConfigMap("fm-caps-fabric", namespace, "fabric-caps-1")
		Expect(k8sClient.Create(ctx, fabricCM)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, fabricCM) }()

		k8sCM := newK8sManagerConfigMap("fm-caps-k8s", namespace, "k8s-caps-1", "ipv4")
		Expect(k8sClient.Create(ctx, k8sCM)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, k8sCM) }()

		disc, err := networkmanager.NewDiscovery(k8sClient, namespace)
		Expect(err).NotTo(HaveOccurred())

		k8sManagerName := "k8s-caps-1"
		nc := &privatev1.NetworkClass{
			Id:            "nc-caps-1",
			FabricManager: ptr.To("fabric-caps-1"),
			K8SManager:    &k8sManagerName,
		}
		var updates []*privatev1.NetworkClass
		stubClient := newListingNetworkClassClient([]*privatev1.NetworkClass{nc}, &updates)
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(stubClient), disc)

		reconciler := NewNetworkClassCapabilitiesReconciler(stubClient, resolver, namespace)
		_, err = reconciler.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())

		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetId()).To(Equal("nc-caps-1"))
		Expect(updates[0].GetCapabilities().GetSupportsIpv4()).To(BeTrue())
		Expect(updates[0].GetCapabilities().GetSupportsIpv6()).To(BeFalse())
	})

	It("does not update the NetworkClass when computed capabilities already match", func() {
		fabricCM := newFabricManagerConfigMap("fm-caps-fabric-noop", namespace, "fabric-caps-noop")
		Expect(k8sClient.Create(ctx, fabricCM)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, fabricCM) }()

		disc, err := networkmanager.NewDiscovery(k8sClient, namespace)
		Expect(err).NotTo(HaveOccurred())

		nc := &privatev1.NetworkClass{
			Id:            "nc-caps-noop",
			FabricManager: ptr.To("fabric-caps-noop"),
			Capabilities:  &privatev1.NetworkClassCapabilities{SupportsIpv4: true},
		}
		var updates []*privatev1.NetworkClass
		stubClient := newListingNetworkClassClient([]*privatev1.NetworkClass{nc}, &updates)
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(stubClient), disc)

		reconciler := NewNetworkClassCapabilitiesReconciler(stubClient, resolver, namespace)
		_, err = reconciler.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())

		Expect(updates).To(BeEmpty())
	})

	It("skips a NetworkClass with no fabricManager set without returning an error", func() {
		disc, err := networkmanager.NewDiscovery(k8sClient, namespace)
		Expect(err).NotTo(HaveOccurred())

		nc := &privatev1.NetworkClass{Id: "nc-caps-no-fabric"}
		var updates []*privatev1.NetworkClass
		stubClient := newListingNetworkClassClient([]*privatev1.NetworkClass{nc}, &updates)
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(stubClient), disc)

		reconciler := NewNetworkClassCapabilitiesReconciler(stubClient, resolver, namespace)
		_, err = reconciler.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(updates).To(BeEmpty())
	})

	It("skips a NetworkClass referencing an unregistered manager without returning an error", func() {
		disc, err := networkmanager.NewDiscovery(k8sClient, namespace)
		Expect(err).NotTo(HaveOccurred())

		nc := &privatev1.NetworkClass{Id: "nc-caps-bad-fabric", FabricManager: ptr.To("unregistered-fabric")}
		var updates []*privatev1.NetworkClass
		stubClient := newListingNetworkClassClient([]*privatev1.NetworkClass{nc}, &updates)
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(stubClient), disc)

		reconciler := NewNetworkClassCapabilitiesReconciler(stubClient, resolver, namespace)
		_, err = reconciler.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(updates).To(BeEmpty())
	})

	It("continues syncing other NetworkClasses when one fails to resolve", func() {
		fabricCM := newFabricManagerConfigMap("fm-caps-fabric-multi", namespace, "fabric-caps-multi")
		Expect(k8sClient.Create(ctx, fabricCM)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, fabricCM) }()

		disc, err := networkmanager.NewDiscovery(k8sClient, namespace)
		Expect(err).NotTo(HaveOccurred())

		goodNC := &privatev1.NetworkClass{Id: "nc-caps-good", FabricManager: ptr.To("fabric-caps-multi")}
		badNC := &privatev1.NetworkClass{Id: "nc-caps-bad", FabricManager: ptr.To("unregistered-fabric-multi")}
		var updates []*privatev1.NetworkClass
		stubClient := newListingNetworkClassClient([]*privatev1.NetworkClass{badNC, goodNC}, &updates)
		resolver := dispatcher.NewResolver(dispatcheradapter.NewNetworkClassAdapter(stubClient), disc)

		reconciler := NewNetworkClassCapabilitiesReconciler(stubClient, resolver, namespace)
		_, err = reconciler.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())

		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetId()).To(Equal("nc-caps-good"))
	})
})

var _ = Describe("listAllNetworkClasses", func() {
	// pagingNetworkClassClient simulates a server that only returns pageSize items per
	// call regardless of the requested limit, so tests can verify the offset-based
	// paging loop rather than relying on a single unbounded List call.
	pagingNetworkClassClient := func(all []*privatev1.NetworkClass, pageSize int) (*stubNetworkClassesClient, *[]int32) {
		var offsetsSeen []int32
		stub := &stubNetworkClassesClient{
			listFunc: func(_ context.Context, in *privatev1.NetworkClassesListRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error) {
				offsetsSeen = append(offsetsSeen, in.GetOffset())
				start := min(int(in.GetOffset()), len(all))
				end := min(start+pageSize, len(all))
				page := all[start:end]
				return &privatev1.NetworkClassesListResponse{
					Items: page,
					Size:  int32(len(page)),
					Total: int32(len(all)),
				}, nil
			},
		}
		return stub, &offsetsSeen
	}

	It("pages through results using offset until every item is fetched", func() {
		all := []*privatev1.NetworkClass{
			{Id: "nc-page-1"}, {Id: "nc-page-2"}, {Id: "nc-page-3"}, {Id: "nc-page-4"}, {Id: "nc-page-5"},
		}
		stub, offsetsSeen := pagingNetworkClassClient(all, 2)

		reconciler := NewNetworkClassCapabilitiesReconciler(stub, nil, "default")
		items, err := reconciler.listAllNetworkClasses(ctx)
		Expect(err).NotTo(HaveOccurred())

		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.GetId())
		}
		Expect(ids).To(Equal([]string{"nc-page-1", "nc-page-2", "nc-page-3", "nc-page-4", "nc-page-5"}))
		// Three round trips: [0,2), [2,4), [4,5) — not a single unbounded call.
		Expect(*offsetsSeen).To(Equal([]int32{0, 2, 4}))
	})

	It("stops after a single call when the server reports no results", func() {
		stub, offsetsSeen := pagingNetworkClassClient(nil, 2)

		reconciler := NewNetworkClassCapabilitiesReconciler(stub, nil, "default")
		items, err := reconciler.listAllNetworkClasses(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(BeEmpty())
		Expect(*offsetsSeen).To(Equal([]int32{0}))
	})

	It("propagates an error from any page without retrying", func() {
		stub := &stubNetworkClassesClient{
			listFunc: func(_ context.Context, _ *privatev1.NetworkClassesListRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error) {
				return nil, fmt.Errorf("fulfillment-service unavailable")
			},
		}

		reconciler := NewNetworkClassCapabilitiesReconciler(stub, nil, "default")
		_, err := reconciler.listAllNetworkClasses(ctx)
		Expect(err).To(MatchError(ContainSubstring("fulfillment-service unavailable")))
	})
})
