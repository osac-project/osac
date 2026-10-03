/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type readinessTenantsClient struct {
	privatev1.TenantsClient
	updates []*privatev1.TenantsUpdateRequest
	err     error
}

func (c *readinessTenantsClient) Update(_ context.Context, req *privatev1.TenantsUpdateRequest, _ ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
	c.updates = append(c.updates, proto.Clone(req).(*privatev1.TenantsUpdateRequest))
	return privatev1.TenantsUpdateResponse_builder{Object: req.GetObject()}.Build(), c.err
}

var _ = Describe("Tenant compute infrastructure readiness", func() {
	const conditionType = privatev1.TenantConditionType_TENANT_CONDITION_TYPE_COMPUTE_INFRASTRUCTURE_READY
	const ready = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	const notReady = privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	const unknown = privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		hubs       *controllers.MockHubsClient
		cache      *controllers.MockHubCache
		tenants    *readinessTenantsClient
		reconciler *function
		tenant     *privatev1.Tenant
		scheme     *runtime.Scheme
	)
	condition := func(object *privatev1.Tenant) *privatev1.TenantCondition {
		for _, c := range object.GetStatus().GetConditions() {
			if c.GetType() == conditionType {
				return c
			}
		}
		return nil
	}
	object := func(phase osacv1alpha1.TenantPhaseType) *osacv1alpha1.Tenant {
		return &osacv1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
			Name: "tenant-a", Namespace: "hub-ns", Labels: map[string]string{labels.TenantID: "api-id"},
		}, Status: osacv1alpha1.TenantStatus{Phase: phase}}
	}
	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		hubs = controllers.NewMockHubsClient(ctrl)
		cache = controllers.NewMockHubCache(ctrl)
		tenants = &readinessTenantsClient{}
		reconciler = &function{logger: logger, hubsClient: hubs, hubCache: cache, tenantsClient: tenants, maskCalculator: masks.NewCalculator().Build()}
		tenant = privatev1.Tenant_builder{Id: "api-id", Metadata: privatev1.Metadata_builder{
			Name: "tenant-a", Tenant: "tenant-a", Version: 7, Finalizers: []string{finalizers.Controller},
		}.Build(), Status: privatev1.TenantStatus_builder{State: privatev1.TenantState_TENANT_STATE_FAILED, Conditions: []*privatev1.TenantCondition{
			privatev1.TenantCondition_builder{Type: privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY, Status: ready}.Build(),
			privatev1.TenantCondition_builder{Type: privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY, Status: ready}.Build(),
		}}.Build()}.Build()
		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	})
	observe := func(clients ...clnt.Client) {
		items := make([]*privatev1.Hub, len(clients))
		for i, client := range clients {
			id := fmt.Sprintf("hub-%d", i)
			items[i] = privatev1.Hub_builder{Id: id}.Build()
			cache.EXPECT().Get(gomock.Any(), id).Return(&controllers.HubEntry{Namespace: "hub-ns", Client: client}, nil)
		}
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{Items: items, Size: int32(len(items)), Total: int32(len(items))}.Build(), nil)
	}
	clientWith := func(objects ...clnt.Object) clnt.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&osacv1alpha1.Tenant{}).WithObjects(objects...).Build()
	}
	check := func(want privatev1.ConditionStatus, reason string) {
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(tenants.updates).To(HaveLen(1))
		req := tenants.updates[0]
		Expect(req.GetLock()).To(BeTrue())
		Expect(req.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		c := condition(req.GetObject())
		Expect(c).NotTo(BeNil())
		Expect(c.GetStatus()).To(Equal(want))
		Expect(c.GetReason()).To(Equal(reason))
		Expect(c.GetLastTransitionTime()).NotTo(BeNil())
		Expect(req.GetObject().GetStatus().GetConditions()).To(HaveLen(3))
		Expect(req.GetObject().GetStatus().GetConditions()[0].GetStatus()).To(Equal(ready))
		Expect(req.GetObject().GetStatus().GetConditions()[1].GetStatus()).To(Equal(ready))
	}
	DescribeTable("reflects the observed phase", func(phase osacv1alpha1.TenantPhaseType, want privatev1.ConditionStatus, reason string) {
		observe(clientWith(object(phase)))
		check(want, reason)
	},
		Entry("ready", osacv1alpha1.TenantPhaseReady, ready, "InfrastructureReady"),
		Entry("progressing", osacv1alpha1.TenantPhaseProgressing, notReady, "InfrastructurePending"),
		Entry("failed", osacv1alpha1.TenantPhaseFailed, notReady, "InfrastructureFailed"),
		Entry("deleting", osacv1alpha1.TenantPhaseDeleting, notReady, "InfrastructureDeleting"),
		Entry("empty", osacv1alpha1.TenantPhaseType(""), notReady, "InfrastructurePending"),
		Entry("unrecognized", osacv1alpha1.TenantPhaseType("future"), notReady, "InfrastructurePending"),
	)
	It("does not treat a deleting Ready CR as ready", func() {
		cr := object(osacv1alpha1.TenantPhaseReady)
		now := metav1.Now()
		cr.DeletionTimestamp = &now
		cr.Finalizers = []string{"test/hold"}
		observe(clientWith(cr))
		check(notReady, "InfrastructureDeleting")
	})
	It("reports no infrastructure when no hubs exist", func() { observe(); check(notReady, "InfrastructureNotProvisioned") })
	It("reports no infrastructure when no matching CR exists", func() { observe(clientWith()); check(notReady, "InfrastructureNotProvisioned") })
	It("ignores another tenant and namespace", func() {
		foreign := object(osacv1alpha1.TenantPhaseReady)
		foreign.Labels[labels.TenantID] = "other-api-id"
		elsewhere := object(osacv1alpha1.TenantPhaseReady)
		elsewhere.Namespace = "other-ns"
		observe(clientWith(foreign, elsewhere))
		check(notReady, "InfrastructureNotProvisioned")
	})
	It("requires every participating hub to be ready", func() {
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)), clientWith(object(osacv1alpha1.TenantPhaseProgressing)))
		check(notReady, "InfrastructurePending")
	})
	It("accepts multiple ready hubs and ignores hubs without a tenant CR", func() {
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)), clientWith(), clientWith(object(osacv1alpha1.TenantPhaseReady)))
		check(ready, "InfrastructureReady")
	})
	It("rejects a label-matching CR with the wrong name", func() {
		wrong := object(osacv1alpha1.TenantPhaseReady)
		wrong.Name = "other"
		observe(clientWith(wrong))
		check(unknown, "InfrastructureStatusUnknown")
	})
	It("bounds stalled observations and still persists unknown readiness", func() {
		blocked := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{List: func(ctx context.Context, _ clnt.WithWatch, _ clnt.ObjectList, _ ...clnt.ListOption) error {
			deadline, ok := ctx.Deadline()
			Expect(ok).To(BeTrue(), "observation must have a deadline")
			Expect(time.Until(deadline)).To(BeNumerically("<=", 10*time.Second))
			<-ctx.Done()
			return ctx.Err()
		}}).Build()
		observe(blocked)
		check(unknown, "InfrastructureStatusUnknown")
		Expect(ctx.Err()).NotTo(HaveOccurred())
	})

	It("rejects ambiguous tenant identity", func() {
		duplicate := object(osacv1alpha1.TenantPhaseReady)
		duplicate.Name = "duplicate"
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady), duplicate))
		check(unknown, "InfrastructureStatusUnknown")
	})
	DescribeTable("reports missing tenant identity as unknown without reading hubs", func(field string) {
		if field == "id" {
			tenant.SetId("")
		} else {
			tenant.GetMetadata().SetName("")
		}
		check(unknown, "InfrastructureStatusUnknown")
	}, Entry("missing API ID", "id"), Entry("missing CR name", "name"))
	DescribeTable("reports incomplete hub configuration as unknown", func(missing string) {
		entry := &controllers.HubEntry{Namespace: "hub-ns", Client: clientWith(object(osacv1alpha1.TenantPhaseReady))}
		switch missing {
		case "entry":
			entry = nil
		case "client":
			entry.Client = nil
		case "namespace":
			entry.Namespace = ""
		}
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub"}.Build()}, Size: 1, Total: 1}.Build(), nil)
		cache.EXPECT().Get(gomock.Any(), "hub").Return(entry, nil)
		check(unknown, "InfrastructureStatusUnknown")
	}, Entry("missing entry", "entry"), Entry("missing client", "client"), Entry("missing namespace", "namespace"))
	DescribeTable("does not claim readiness from inconsistent hub pagination", func(size, total int32) {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub"}.Build()}, Size: size, Total: total}.Build(), nil)
		cache.EXPECT().Get(gomock.Any(), "hub").Return(&controllers.HubEntry{Namespace: "hub-ns", Client: clientWith(object(osacv1alpha1.TenantPhaseReady))}, nil)
		check(unknown, "InfrastructureStatusUnknown")
	}, Entry("negative size", int32(-1), int32(1)), Entry("size differs from items", int32(0), int32(1)), Entry("total smaller than page", int32(1), int32(0)))
	It("reports an inaccessible hub as unknown", func() {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "hub"}.Build()}, Size: 1, Total: 1}.Build(), nil)
		cache.EXPECT().Get(gomock.Any(), "hub").Return(nil, errors.New("private endpoint details"))
		check(unknown, "InfrastructureStatusUnknown")
		Expect(condition(tenants.updates[0].GetObject()).GetMessage()).NotTo(ContainSubstring("private endpoint"))
	})
	DescribeTable("handles failed Kubernetes observations", func(phase osacv1alpha1.TenantPhaseType, want privatev1.ConditionStatus, reason string) {
		broken := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, clnt.WithWatch, clnt.ObjectList, ...clnt.ListOption) error {
			return errors.New("private endpoint")
		}}).Build()
		observe(broken, clientWith(object(phase)))
		check(want, reason)
	}, Entry("unknown despite one ready hub", osacv1alpha1.TenantPhaseReady, unknown, "InfrastructureStatusUnknown"),
		Entry("known not-ready takes precedence", osacv1alpha1.TenantPhaseProgressing, notReady, "InfrastructurePending"))
	It("reports a failed hub listing as unknown", func() {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("list failed"))
		check(unknown, "InfrastructureStatusUnknown")
	})
	It("reads every page of hubs", func() {
		gomock.InOrder(
			hubs.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(req.GetOffset()).To(BeZero())
				return privatev1.HubsListResponse_builder{Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "a"}.Build()}, Size: 1, Total: 2}.Build(), nil
			}),
			hubs.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *privatev1.HubsListRequest, _ ...grpc.CallOption) (*privatev1.HubsListResponse, error) {
				Expect(req.GetOffset()).To(Equal(int32(1)))
				return privatev1.HubsListResponse_builder{Items: []*privatev1.Hub{privatev1.Hub_builder{Id: "b"}.Build()}, Size: 1, Total: 2}.Build(), nil
			}),
		)
		cache.EXPECT().Get(gomock.Any(), "a").Return(&controllers.HubEntry{Namespace: "hub-ns", Client: clientWith(object(osacv1alpha1.TenantPhaseReady))}, nil)
		cache.EXPECT().Get(gomock.Any(), "b").Return(&controllers.HubEntry{Namespace: "hub-ns", Client: clientWith(object(osacv1alpha1.TenantPhaseFailed))}, nil)
		check(notReady, "InfrastructureFailed")
	})
	It("does not loop on an empty incomplete page", func() {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{Total: 1}.Build(), nil)
		check(unknown, "InfrastructureStatusUnknown")
	})
	It("does not rewrite an unchanged observation", func() {
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		check(ready, "InfrastructureReady")
		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(tenants.updates).To(BeEmpty())
	})
	It("preserves transition time when only the explanation changes", func() {
		before := timestamppb.New(time.Now().Add(-time.Hour))
		tenant.GetStatus().SetConditions(append(tenant.GetStatus().GetConditions(), privatev1.TenantCondition_builder{Type: conditionType, Status: notReady, Reason: new("InfrastructurePending"), LastTransitionTime: before}.Build()))
		observe(clientWith(object(osacv1alpha1.TenantPhaseFailed)))
		check(notReady, "InfrastructureFailed")
		Expect(proto.Equal(condition(tenants.updates[0].GetObject()).GetLastTransitionTime(), before)).To(BeTrue())
	})
	DescribeTable("replaces previously ready observations", func(failedRead bool) {
		before := timestamppb.New(time.Now().Add(-time.Hour))
		tenant.GetStatus().SetConditions(append(tenant.GetStatus().GetConditions(), privatev1.TenantCondition_builder{Type: conditionType, Status: ready, LastTransitionTime: before}.Build()))
		if failedRead {
			hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("offline"))
			check(unknown, "InfrastructureStatusUnknown")
		} else {
			observe(clientWith(object(osacv1alpha1.TenantPhaseProgressing)))
			check(notReady, "InfrastructurePending")
		}
		Expect(condition(tenants.updates[0].GetObject()).GetLastTransitionTime().AsTime()).To(BeTemporally(">", before.AsTime()))
	}, Entry("lost readiness", false), Entry("unknown readiness", true))
	It("propagates version conflicts without an unlocked retry", func() {
		tenants.err = status.Error(codes.Aborted, "version changed")
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		Expect(status.Code(reconciler.Run(ctx, tenant))).To(Equal(codes.Aborted))
		Expect(tenants.updates).To(HaveLen(1))
		Expect(tenants.updates[0].GetLock()).To(BeTrue())
	})
	It("persists only compute readiness when other lifecycle work fails", func() {
		tenant.GetMetadata().SetTenant("")
		tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_UNSPECIFIED)
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		Expect(reconciler.Run(ctx, tenant)).To(MatchError(ContainSubstring("metadata.tenant")))
		Expect(tenants.updates).To(HaveLen(1))
		saved := tenants.updates[0].GetObject()
		Expect(saved.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_UNSPECIFIED))
		Expect(condition(saved).GetStatus()).To(Equal(ready))
		Expect(tenants.updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("status.conditions"))
	})
	It("observes infrastructure while adding the initial finalizer", func() {
		tenant.GetMetadata().SetFinalizers(nil)
		tenant.ClearStatus()
		observe(clientWith())
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(condition(tenants.updates[0].GetObject()).GetStatus()).To(Equal(notReady))
		Expect(tenants.updates[0].GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})
	DescribeTable("does not exempt reserved tenants", func(name string) {
		tenant.GetMetadata().SetName(name)
		observe(clientWith())
		check(notReady, "InfrastructureNotProvisioned")
	}, Entry("system", "system"), Entry("shared", "shared"))
	DescribeTable("persists readiness despite subsystem failures", func(subsystem string) {
		tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_SYNCED)
		tenant.GetStatus().SetIdpTenantName("tenant-a")
		idpClient := idp.NewMockClientInterface(ctrl)
		manager, err := idp.NewTenantManager().SetLogger(logger).SetClient(idpClient).Build()
		Expect(err).NotTo(HaveOccurred())
		reconciler.idpManager = manager
		failure := errors.New("subsystem unavailable")
		if subsystem == "IDP" {
			idpClient.EXPECT().GetTenant(gomock.Any(), "tenant-a").Return(nil, failure)
		} else {
			idpClient.EXPECT().GetTenant(gomock.Any(), "tenant-a").Return(&idp.Tenant{Name: "tenant-a"}, nil)
			if subsystem == "vault" {
				tenant.GetStatus().GetConditions()[0].SetStatus(notReady)
				vaultClient := vault.NewMockLifecycleClient(ctrl)
				reconciler.vaultLifecycle = vaultClient
				vaultClient.EXPECT().EnsureTenantNamespace(gomock.Any(), "tenant-a").Return(failure)
			} else {
				vn := NewMockVirtualNetworksClient(ctrl)
				reconciler.virtualNetworksClient = vn
				vn.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, failure)
			}
		}
		original := proto.Clone(tenant).(*privatev1.Tenant)
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		Expect(reconciler.Run(ctx, tenant)).To(MatchError(ContainSubstring("subsystem unavailable")))
		Expect(tenants.updates).To(HaveLen(1))
		saved := tenants.updates[0].GetObject()
		Expect(saved.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		Expect(condition(saved).GetStatus()).To(Equal(ready))
		Expect(proto.Equal(saved.GetStatus().GetConditions()[0], original.GetStatus().GetConditions()[0])).To(BeTrue())
	}, Entry("IDP error", "IDP"), Entry("vault error", "vault"), Entry("network error", "network"))
	It("does not poll infrastructure during tenant deletion", func() {
		tenant.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		projects := NewMockProjectsClient(ctrl)
		reconciler.projectsClient = projects
		projects.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("deletion pending"))
		Expect(reconciler.Run(ctx, tenant)).To(MatchError(ContainSubstring("deletion pending")))
		Expect(tenants.updates).To(BeEmpty())
	})
	It("ignores legacy namespace readiness conditions", func() {
		cr := object(osacv1alpha1.TenantPhaseReady)
		cr.Status.Conditions = []metav1.Condition{{Type: string(osacv1alpha1.TenantConditionNamespaceReady), Status: metav1.ConditionFalse}}
		observe(clientWith(cr))
		check(ready, "InfrastructureReady")
		Expect(condition(tenants.updates[0].GetObject()).GetMessage()).NotTo(ContainSubstring("namespace"))
	})
	It("recovers from unknown to ready", func() {
		tenant.GetStatus().SetConditions(append(tenant.GetStatus().GetConditions(), privatev1.TenantCondition_builder{Type: conditionType, Status: unknown, Reason: new("InfrastructureStatusUnknown")}.Build()))
		observe(clientWith(object(osacv1alpha1.TenantPhaseReady)))
		check(ready, "InfrastructureReady")
	})

	It("requires a hub cache in the builder", func() {
		conn, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conn.Close)
		manager, err := idp.NewTenantManager().SetLogger(logger).SetClient(idp.NewMockClientInterface(ctrl)).Build()
		Expect(err).NotTo(HaveOccurred())
		builder := NewFunction().SetLogger(logger).SetConnection(conn).SetIdpManager(manager).SetVaultLifecycle(vault.NewMockLifecycleClient(ctrl))
		_, err = builder.Build()
		Expect(err).To(MatchError("hub cache is mandatory"))
		_, err = builder.SetHubCache(cache).Build()
		Expect(err).NotTo(HaveOccurred())
	})
})
