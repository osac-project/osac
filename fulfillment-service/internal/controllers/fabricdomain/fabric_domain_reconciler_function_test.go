/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fabricdomain

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestFabricDomain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FabricDomain fulfillment reconciler")
}

var _ = Describe("FabricDomain fulfillment reconciler", func() {
	const (
		hubID       = "vn-hub"
		namespace   = "networking"
		tenant      = "tenant-a"
		domainID    = "domain-id"
		vnID        = "vn-id"
		ownerKey    = "osac.openshift.io/owner-reference"
		crFinalizer = "osac.openshift.io/fabricdomain-finalizer"
	)
	var (
		ctx         context.Context
		r           *function
		domain      *privatev1.FabricDomain
		vn          *privatev1.VirtualNetwork
		client      clnt.Client
		hubCache    *controllers.MockHubCache
		domains     *MockFabricDomainsClient
		networks    *MockVirtualNetworksClient
		hubs        *controllers.MockHubsClient
		updates     []*privatev1.FabricDomainsUpdateRequest
		updateError error
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		hubCache = controllers.NewMockHubCache(ctrl)
		domains = NewMockFabricDomainsClient(ctrl)
		networks = NewMockVirtualNetworksClient(ctrl)
		hubs = controllers.NewMockHubsClient(ctrl)
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		client = fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&osacv1alpha1.FabricDomain{}).Build()
		r = &function{
			logger: slog.New(slog.NewTextHandler(GinkgoWriter, nil)), hubCache: hubCache,
			fabricDomainsClient: domains, virtualNetworksClient: networks, hubsClient: hubs,
			maskCalculator: masks.NewCalculator().Build(),
		}
		domain = privatev1.FabricDomain_builder{
			Id: domainID,
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant, Name: "fabric-a", Finalizers: []string{finalizers.Controller, "other-controller"},
				Annotations: map[string]string{ownerKey: "supplied-owner"},
			}.Build(),
			Spec: privatev1.FabricDomainSpec_builder{
				Type:    privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
				Servers: []string{"server-a"}, VirtualNetwork: vnID,
			}.Build(),
			Status: privatev1.FabricDomainStatus_builder{Hub: hubID}.Build(),
		}.Build()
		vn = privatev1.VirtualNetwork_builder{
			Id: vnID, Metadata: privatev1.Metadata_builder{Tenant: tenant}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{Hub: hubID}.Build(),
		}.Build()
		updates = nil
		updateError = nil
		domains.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.FabricDomainsUpdateRequest, _ ...grpc.CallOption) (*privatev1.FabricDomainsUpdateResponse, error) {
				updates = append(updates, proto.Clone(request).(*privatev1.FabricDomainsUpdateRequest))
				return privatev1.FabricDomainsUpdateResponse_builder{Object: request.GetObject()}.Build(), updateError
			}).AnyTimes()
	})

	allowVN := func() {
		networks.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.VirtualNetworksGetRequest, _ ...grpc.CallOption) (*privatev1.VirtualNetworksGetResponse, error) {
				Expect(request.GetId()).To(Equal(vnID))
				return privatev1.VirtualNetworksGetResponse_builder{Object: vn}.Build(), nil
			}).AnyTimes()
	}
	allowHub := func() {
		hubCache.EXPECT().Get(gomock.Any(), hubID).
			DoAndReturn(func(context.Context, string) (*controllers.HubEntry, error) {
				return &controllers.HubEntry{Namespace: namespace, Client: client}, nil
			}).AnyTimes()
	}
	allowEmptyHubSearch := func() {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{}, nil)
	}
	listCRs := func() []osacv1alpha1.FabricDomain {
		list := &osacv1alpha1.FabricDomainList{}
		Expect(client.List(ctx, list, clnt.InNamespace(namespace))).To(Succeed())
		return list.Items
	}
	newCR := func(name string) *osacv1alpha1.FabricDomain {
		return &osacv1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: namespace, Finalizers: []string{crFinalizer},
				Labels:      map[string]string{labels.FabricDomainUuid: domainID},
				Annotations: map[string]string{annotations.Tenant: tenant, ownerKey: "supplied-owner"},
			},
			Spec: osacv1alpha1.FabricDomainSpec{
				Type: osacv1alpha1.FabricDomainTypeEthernetEW, Servers: []string{"server-a"}, VirtualNetwork: vnID,
			},
		}
	}

	It("persists its finalizer before reading placement or creating a CR", func() {
		domain.GetMetadata().SetFinalizers([]string{"other-controller"})
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("metadata.finalizers"))
		Expect(updates[0].GetLock()).To(BeTrue())
		Expect(domain.GetMetadata().GetFinalizers()).To(ConsistOf("other-controller", finalizers.Controller))
		Expect(listCRs()).To(BeEmpty())
	})

	It("does no hub work if saving the finalizer fails", func() {
		domain.GetMetadata().SetFinalizers(nil)
		updateError = errors.New("concurrent update")
		Expect(r.run(ctx, domain)).To(MatchError(updateError))
		Expect(listCRs()).To(BeEmpty())
	})

	It("persists the VN hub before creating the CR on a later pass", func() {
		allowVN()
		allowHub()
		allowEmptyHubSearch()
		domain.GetStatus().SetHub("")
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("status.hub"))
		Expect(listCRs()).To(BeEmpty())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(listCRs()).To(HaveLen(1))
		Expect(updates).To(HaveLen(1))
	})

	It("does not create a CR if saving placement fails", func() {
		allowVN()
		allowHub()
		allowEmptyHubSearch()
		domain.GetStatus().SetHub("")
		updateError = errors.New("connection lost")
		Expect(r.run(ctx, domain)).To(MatchError(updateError))
		Expect(listCRs()).To(BeEmpty())
	})

	It("creates and resizes one CR with authoritative isolation metadata, preserving finalizers and operator status", func() {
		allowVN()
		allowHub()
		Expect(r.run(ctx, domain)).To(Succeed())
		objects := listCRs()
		Expect(objects).To(HaveLen(1))
		object := objects[0].DeepCopy()
		Expect(object.Labels[labels.FabricDomainUuid]).To(Equal(domainID))
		Expect(object.Annotations[annotations.Tenant]).To(Equal(tenant))
		Expect(object.Annotations[ownerKey]).To(Equal(vnID))
		Expect(object.OwnerReferences).To(BeEmpty())
		Expect(object.Spec.VirtualNetwork).To(Equal(vnID))
		Expect(object.Spec.Type).To(Equal(osacv1alpha1.FabricDomainTypeEthernetEW))
		object.Finalizers = []string{crFinalizer}
		object.Annotations["operator.example/annotation"] = "preserve"
		object.Annotations[ownerKey] = "stale-cr-owner"
		Expect(client.Update(ctx, object)).To(Succeed())
		object.Status.BackendID = "42"
		Expect(client.Status().Update(ctx, object)).To(Succeed())
		domain.GetSpec().SetServers([]string{"server-a", "server-b"})
		Expect(r.run(ctx, domain)).To(Succeed())
		objects = listCRs()
		Expect(objects).To(HaveLen(1))
		Expect(objects[0].Name).To(Equal(object.Name))
		Expect(objects[0].Spec.Servers).To(Equal([]string{"server-a", "server-b"}))
		Expect(objects[0].Status.BackendID).To(Equal("42"))
		Expect(objects[0].Finalizers).To(ConsistOf(crFinalizer))
		Expect(objects[0].Annotations["operator.example/annotation"]).To(Equal("preserve"))
		Expect(objects[0].Annotations[ownerKey]).To(Equal(vnID))
		Expect(updates).To(BeEmpty())
		version := objects[0].ResourceVersion
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(listCRs()[0].ResourceVersion).To(Equal(version))
	})

	DescribeTable("creates a domain with authoritative tenant and VN owner annotations",
		func(supplied map[string]string) {
			allowVN()
			allowHub()
			suppliedOwner := supplied[ownerKey]
			domain.GetMetadata().SetAnnotations(supplied)
			Expect(r.run(ctx, domain)).To(Succeed())
			objects := listCRs()
			Expect(objects).To(HaveLen(1))
			Expect(objects[0].Annotations).To(HaveKeyWithValue(annotations.Tenant, tenant))
			Expect(objects[0].Annotations).To(HaveKeyWithValue(ownerKey, vnID))
			Expect(objects[0].OwnerReferences).To(BeEmpty())
			Expect(objects[0].Labels).To(HaveKeyWithValue(labels.FabricDomainUuid, domainID))
			Expect(domain.GetMetadata().GetAnnotations()[ownerKey]).To(Equal(suppliedOwner), "CR annotations must not mutate API metadata")
			Expect(updates).To(BeEmpty())
		},
		Entry("no supplied annotations", nil),
		Entry("missing owner and stale tenant", map[string]string{annotations.Tenant: "stale-tenant"}),
		Entry("empty supplied owner", map[string]string{ownerKey: ""}),
		Entry("incorrect supplied owner is replaced", map[string]string{ownerKey: "caller-other-vn"}),
	)

	It("adds the VN owner annotation to an existing CR without changing its cleanup lifecycle", func() {
		allowVN()
		allowHub()
		domain.GetMetadata().SetAnnotations(nil)
		object := newCR("existing")
		delete(object.Annotations, ownerKey)
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.Annotations).To(HaveKeyWithValue(annotations.Tenant, tenant))
		Expect(object.Annotations).To(HaveKeyWithValue(ownerKey, vnID))
		Expect(object.OwnerReferences).To(BeEmpty())
		Expect(object.Finalizers).To(ConsistOf(crFinalizer))
		Expect(updates).To(BeEmpty())
	})

	DescribeTable("refuses unsafe or incomplete VN placement",
		func(change func(*privatev1.FabricDomain, *privatev1.VirtualNetwork), message string) {
			allowVN()
			change(domain, vn)
			Expect(r.run(ctx, domain)).To(MatchError(ContainSubstring(message)))
			Expect(listCRs()).To(BeEmpty())
			Expect(updates).To(BeEmpty())
		},
		Entry("no tenant", func(fd *privatev1.FabricDomain, _ *privatev1.VirtualNetwork) { fd.GetMetadata().SetTenant("") }, "tenant assigned"),
		Entry("different tenant", func(_ *privatev1.FabricDomain, vn *privatev1.VirtualNetwork) { vn.GetMetadata().SetTenant("tenant-b") }, "same tenant"),
		Entry("deleting VN", func(_ *privatev1.FabricDomain, vn *privatev1.VirtualNetwork) {
			vn.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}, "missing or deleting"),
		Entry("no VN hub", func(_ *privatev1.FabricDomain, vn *privatev1.VirtualNetwork) { vn.GetStatus().SetHub("") }, "waiting for virtual network"),
		Entry("changed VN hub", func(_ *privatev1.FabricDomain, vn *privatev1.VirtualNetwork) { vn.GetStatus().SetHub("different-hub") }, "recorded hub"),
	)

	It("rejects unsupported fabric types instead of silently mapping them to Ethernet", func() {
		allowVN()
		allowHub()
		domain.GetSpec().SetType(privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW)
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(listCRs()).To(BeEmpty())
		Expect(updates).To(HaveLen(1))
		Expect(domain.GetStatus().GetConditions()[0].GetType()).To(Equal(privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_FAILED))
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(updates).To(HaveLen(1), "unchanged failures must not produce an event loop")
	})

	It("records Kubernetes validation failures in conditions", func() {
		allowVN()
		allowHub()
		client = interceptor.NewClient(client.(clnt.WithWatch), interceptor.Funcs{
			Create: func(context.Context, clnt.WithWatch, clnt.Object, ...clnt.CreateOption) error {
				return apierrors.NewInvalid(schema.GroupKind{Group: "osac.openshift.io", Kind: "FabricDomain"}, "domain",
					field.ErrorList{field.Invalid(field.NewPath("spec", "servers"), nil, "servers are required")})
			},
		})
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(updates).To(HaveLen(1))
		Expect(domain.GetStatus().GetConditions()[0].GetMessage()).To(ContainSubstring("servers are required"))
	})

	It("does not patch a CR whose deletion is still in progress", func() {
		allowVN()
		allowHub()
		object := newCR("deleting")
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(client.Delete(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(MatchError(ContainSubstring("still being deleted")))
		Expect(listCRs()).To(HaveLen(1))
	})

	It("keeps the DB finalizer through CR deletion and removes only its own finalizer after the CR disappears", func() {
		allowHub()
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		object := newCR("existing")
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(updates).To(BeEmpty())
		object.Finalizers = nil
		Expect(client.Update(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(domain.GetMetadata().GetFinalizers()).To(ConsistOf("other-controller"))
		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("metadata.finalizers"))
	})

	It("refuses cleanup without a tenant instead of adopting an unannotated CR", func() {
		domain.GetMetadata().SetTenant("")
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		object := newCR("unannotated")
		delete(object.Annotations, annotations.Tenant)
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(MatchError(ContainSubstring("tenant assigned")))
		Expect(listCRs()[0].DeletionTimestamp.IsZero()).To(BeTrue())
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(updates).To(BeEmpty())
	})

	It("releases unplaced domains only after an empty registered-hub search", func() {
		allowEmptyHubSearch()
		domain.GetStatus().SetHub("")
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(domain.GetMetadata().GetFinalizers()).To(ConsistOf("other-controller"))
	})

	It("recovers deletion placement from a later hub page before deleting the CR", func() {
		domain.GetStatus().SetHub("")
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		object := newCR("existing")
		Expect(client.Create(ctx, object)).To(Succeed())
		emptyClient := fake.NewClientBuilder().WithScheme(client.Scheme()).Build()
		hubCache.EXPECT().Get(gomock.Any(), "empty-hub").Return(&controllers.HubEntry{Namespace: namespace, Client: emptyClient}, nil)
		allowHub()
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(request.GetOffset()).To(BeZero())
				return &privatev1.HubsListResponse{Total: 2, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "empty-hub"}.Build()}}, nil
			})
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(request.GetOffset()).To(Equal(int32(1)))
				return &privatev1.HubsListResponse{Total: 2, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: hubID}.Build()}}, nil
			})
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(domain.GetStatus().GetHub()).To(Equal(hubID))
		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("status.hub"))
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.DeletionTimestamp.IsZero()).To(BeTrue(), "placement must be saved before deletion")
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	DescribeTable("does not treat a failed or ambiguous all-hub search as absence",
		func(failSecond bool) {
			domain.GetStatus().SetHub("")
			domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			Expect(client.Create(ctx, newCR("existing"))).To(Succeed())
			hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{
				Total: 2, Size: 2, Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: hubID}.Build(), privatev1.Hub_builder{Id: "second-hub"}.Build(),
				},
			}, nil)
			allowHub()
			if failSecond {
				hubCache.EXPECT().Get(gomock.Any(), "second-hub").Return(nil, errors.New("unreachable second hub"))
			} else {
				hubCache.EXPECT().Get(gomock.Any(), "second-hub").Return(&controllers.HubEntry{Namespace: namespace, Client: client}, nil)
			}
			Expect(r.run(ctx, domain)).NotTo(Succeed())
			Expect(domain.GetStatus().GetHub()).To(BeEmpty())
			Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			Expect(updates).To(BeEmpty())
			Expect(listCRs()[0].DeletionTimestamp.IsZero()).To(BeTrue())
		},
		Entry("one matching hub and another unreachable", true),
		Entry("matching CRs on two hubs", false),
	)

	It("retains the finalizer if listing hubs fails", func() {
		domain.GetStatus().SetHub("")
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		expected := errors.New("cannot list hubs")
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, expected)
		Expect(r.run(ctx, domain)).To(MatchError(expected))
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(updates).To(BeEmpty())
	})

	It("retains the finalizer if hub pagination ends before the reported total", func() {
		domain.GetStatus().SetHub("")
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{Total: 1}, nil)
		Expect(r.run(ctx, domain)).To(MatchError(ContainSubstring("incomplete page")))
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(updates).To(BeEmpty())
	})

	It("recovers an existing CR before choosing a hub for an unplaced active domain", func() {
		domain.GetStatus().SetHub("")
		Expect(client.Create(ctx, newCR("existing"))).To(Succeed())
		allowHub()
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(&privatev1.HubsListResponse{
			Total: 1, Size: 1, Items: []*privatev1.Hub{privatev1.Hub_builder{Id: hubID}.Build()},
		}, nil)
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(domain.GetStatus().GetHub()).To(Equal(hubID))
		Expect(listCRs()).To(HaveLen(1))
		Expect(updates).To(HaveLen(1))
	})

	It("releases the finalizer for a decommissioned hub following the shared networking policy", func() {
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		hubCache.EXPECT().Get(gomock.Any(), hubID).Return(nil, controllers.ErrHubNotFound)
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(domain.GetMetadata().GetFinalizers()).To(ConsistOf("other-controller"))
	})

	It("retains the finalizer when the recorded hub is temporarily unavailable", func() {
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		expected := errors.New("temporary connection failure")
		hubCache.EXPECT().Get(gomock.Any(), hubID).Return(nil, expected)
		Expect(r.run(ctx, domain)).To(MatchError(expected))
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(updates).To(BeEmpty())
	})

	DescribeTable("retains the DB finalizer on Kubernetes cleanup errors",
		func(failList bool) {
			allowHub()
			domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			Expect(client.Create(ctx, newCR("existing"))).To(Succeed())
			expected := errors.New("Kubernetes temporarily unavailable")
			client = interceptor.NewClient(client.(clnt.WithWatch), interceptor.Funcs{
				List: func(ctx context.Context, inner clnt.WithWatch, list clnt.ObjectList, opts ...clnt.ListOption) error {
					if failList {
						return expected
					}
					return inner.List(ctx, list, opts...)
				},
				Delete: func(context.Context, clnt.WithWatch, clnt.Object, ...clnt.DeleteOption) error {
					return expected
				},
			})
			Expect(r.run(ctx, domain)).To(MatchError(expected))
			Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			Expect(updates).To(BeEmpty())
		},
		Entry("list failure", true),
		Entry("delete failure", false),
	)

	It("retries transient CR creation failures without replacing operator-owned status", func() {
		allowVN()
		allowHub()
		expected := errors.New("Kubernetes temporarily unavailable")
		client = interceptor.NewClient(client.(clnt.WithWatch), interceptor.Funcs{
			Create: func(context.Context, clnt.WithWatch, clnt.Object, ...clnt.CreateOption) error {
				return expected
			},
		})
		before := proto.Clone(domain.GetStatus())
		Expect(r.run(ctx, domain)).To(MatchError(expected))
		Expect(proto.Equal(before, domain.GetStatus())).To(BeTrue())
		Expect(updates).To(BeEmpty())
	})

	It("refuses ambiguous UUID matches before deleting anything", func() {
		allowHub()
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		Expect(client.Create(ctx, newCR("first"))).To(Succeed())
		Expect(client.Create(ctx, newCR("second"))).To(Succeed())
		Expect(r.run(ctx, domain)).To(MatchError(ContainSubstring("found 2")))
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(listCRs()).To(HaveLen(2))
	})

	It("does not use a FabricDomain tenant annotation as identity for cleanup", func() {
		allowHub()
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		object := newCR("existing")
		object.Annotations[annotations.Tenant] = "tenant-b"
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(domain.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("ignores matching UUIDs outside the assigned hub namespace", func() {
		allowHub()
		domain.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		object := newCR("other-namespace")
		object.Namespace = "elsewhere"
		Expect(client.Create(ctx, object)).To(Succeed())
		Expect(r.run(ctx, domain)).To(Succeed())
		Expect(client.Get(ctx, clnt.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.DeletionTimestamp.IsZero()).To(BeTrue())
		Expect(domain.GetMetadata().GetFinalizers()).To(ConsistOf("other-controller"))
	})

	It("builds the generic event-driven reconciler with the FabricDomain payload", func() {
		connection, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)
		fn, err := NewFunction().SetLogger(r.logger).SetConnection(connection).SetHubCache(hubCache).Build()
		Expect(err).NotTo(HaveOccurred())
		_, err = controllers.NewReconciler[*privatev1.FabricDomain]().SetLogger(r.logger).
			SetName("fabric_domain").SetClient(connection).SetFunction(fn).
			SetEventFilter("has(event.fabric_domain) || (has(event.virtual_network) && event.type == EVENT_TYPE_OBJECT_UPDATED) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").Build()
		Expect(err).NotTo(HaveOccurred())
	})
})
