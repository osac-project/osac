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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Tenant storage onboarding feedback", func() {
	const conditionType = privatev1.TenantConditionType_TENANT_CONDITION_TYPE_STORAGE_ONBOARDING_COMPLETE
	const sourceType = "StorageOnboardingComplete"
	const complete = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	const pending = privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	const unknown = privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	var (
		ctx        context.Context
		hubs       *controllers.MockHubsClient
		cache      *controllers.MockHubCache
		tenants    *readinessTenantsClient
		reconciler *function
		tenant     *privatev1.Tenant
		scheme     *runtime.Scheme
		transition time.Time
	)
	condition := func(object *privatev1.Tenant) *privatev1.TenantCondition {
		for _, c := range object.GetStatus().GetConditions() {
			if c.GetType() == conditionType {
				return c
			}
		}
		return nil
	}
	object := func(status metav1.ConditionStatus) *osacv1alpha1.Tenant {
		return &osacv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tenant-a", Namespace: "hub-ns", Generation: 8,
				Labels: map[string]string{labels.TenantID: "api-id"},
			},
			Status: osacv1alpha1.TenantStatus{
				Phase: osacv1alpha1.TenantPhaseReady,
				Conditions: []metav1.Condition{{
					Type: sourceType, Status: status, Reason: "OperatorEvidence",
					Message: "Operator supplied storage result", ObservedGeneration: 3,
					LastTransitionTime: metav1.NewTime(transition),
				}},
			},
		}
	}
	BeforeEach(func() {
		ctx = context.Background()
		ctrl := gomock.NewController(GinkgoT())
		hubs = controllers.NewMockHubsClient(ctrl)
		cache = controllers.NewMockHubCache(ctrl)
		tenants = &readinessTenantsClient{}
		reconciler = &function{logger: logger, hubsClient: hubs, hubCache: cache,
			tenantsClient: tenants, maskCalculator: masks.NewCalculator().Build()}
		tenant = privatev1.Tenant_builder{
			Id: "api-id", Metadata: privatev1.Metadata_builder{
				Name: "tenant-a", Tenant: "tenant-a", Version: 7,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_FAILED,
				Conditions: []*privatev1.TenantCondition{
					privatev1.TenantCondition_builder{Type: privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY, Status: complete}.Build(),
					privatev1.TenantCondition_builder{Type: privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY, Status: complete}.Build(),
				},
			}.Build(),
		}.Build()
		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		transition = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	})
	clientWith := func(objects ...clnt.Object) clnt.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&osacv1alpha1.Tenant{}).WithObjects(objects...).Build()
	}
	observe := func(clients ...clnt.Client) {
		items := make([]*privatev1.Hub, len(clients))
		for i, client := range clients {
			id := fmt.Sprintf("hub-%d", i)
			items[i] = privatev1.Hub_builder{Id: id}.Build()
			cache.EXPECT().Get(gomock.Any(), id).Return(&controllers.HubEntry{Namespace: "hub-ns", Client: client}, nil)
		}
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(privatev1.HubsListResponse_builder{
			Items: items, Size: int32(len(items)), Total: int32(len(items)),
		}.Build(), nil)
	}
	check := func(want privatev1.ConditionStatus) *privatev1.TenantCondition {
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(tenants.updates).To(HaveLen(1))
		saved := tenants.updates[0].GetObject()
		Expect(tenants.updates[0].GetLock()).To(BeTrue())
		Expect(saved.GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(saved.GetStatus().GetConditions()[0].GetStatus()).To(Equal(complete))
		Expect(saved.GetStatus().GetConditions()[1].GetStatus()).To(Equal(complete))
		c := condition(saved)
		Expect(c).NotTo(BeNil())
		Expect(c.GetStatus()).To(Equal(want))
		return c
	}

	It("defines a private storage condition without exposing public Tenant status", func() {
		value := conditionType.Descriptor().Values().ByNumber(protoreflect.EnumNumber(conditionType))
		Expect(value).NotTo(BeNil())
		Expect(string(value.Name())).To(Equal("TENANT_CONDITION_TYPE_STORAGE_ONBOARDING_COMPLETE"))
		Expect((&publicv1.Tenant{}).ProtoReflect().Descriptor().Fields().ByName("status")).To(BeNil())
	})
	DescribeTable("copies the operator condition exactly", func(status metav1.ConditionStatus, want privatev1.ConditionStatus) {
		observe(clientWith(object(status)))
		c := check(want)
		Expect(c.GetReason()).To(Equal("OperatorEvidence"))
		Expect(c.GetMessage()).To(Equal("Operator supplied storage result"))
		Expect(proto.Equal(c.GetLastTransitionTime(), timestamppb.New(transition))).To(BeTrue())
	}, Entry("False", metav1.ConditionFalse, pending), Entry("Unknown", metav1.ConditionUnknown, unknown), Entry("latched True from an earlier generation", metav1.ConditionTrue, complete))
	DescribeTable("does not derive storage readiness from infrastructure readiness", func(phase osacv1alpha1.TenantPhaseType) {
		cr := object(metav1.ConditionTrue)
		cr.Status.Phase = phase
		observe(clientWith(cr))
		check(complete)
	}, Entry("progressing", osacv1alpha1.TenantPhaseProgressing), Entry("failed", osacv1alpha1.TenantPhaseFailed))
	DescribeTable("keeps missing operator evidence incomplete", func(missing string) {
		switch missing {
		case "hubs":
			observe()
		case "tenant":
			observe(clientWith())
		case "condition":
			cr := object(metav1.ConditionTrue)
			cr.Status.Conditions[0].Type = "StorageBackendReady"
			observe(clientWith(cr))
		}
		c := check(pending)
		Expect(c.GetReason()).To(Equal("StorageOnboardingPending"))
	}, Entry("no hubs", "hubs"), Entry("no Tenant CR", "tenant"), Entry("legacy storage condition only", "condition"))
	DescribeTable("reports unknown when hub observation is not configured", func(missing string) {
		if missing != "cache" {
			reconciler.hubsClient = nil
		}
		if missing != "client" {
			reconciler.hubCache = nil
		}
		Expect(check(unknown).GetReason()).To(Equal("StorageOnboardingStatusUnknown"))
	}, Entry("missing hubs client", "client"), Entry("missing hub cache", "cache"), Entry("both missing", "both"))
	It("reports unavailable observation after a previous completion without claiming pending onboarding", func() {
		observe(clientWith(object(metav1.ConditionTrue)))
		check(complete)
		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		reconciler.hubsClient = nil
		reconciler.hubCache = nil
		c := check(unknown)
		Expect(c.GetReason()).To(Equal("StorageOnboardingStatusUnknown"))
		Expect(c.GetMessage()).To(Equal("Operator storage onboarding status could not be observed"))
		Expect(c.GetLastTransitionTime().AsTime()).To(BeTemporally(">", transition))

		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(tenants.updates).To(BeEmpty())
		Expect(proto.Equal(condition(tenant).GetLastTransitionTime(), c.GetLastTransitionTime())).To(BeTrue())
	})
	It("uses the only reported storage condition across hubs", func() {
		legacy := object(metav1.ConditionTrue)
		legacy.Status.Conditions = nil
		observe(clientWith(legacy), clientWith(object(metav1.ConditionTrue)), clientWith())
		check(complete)
	})
	It("reports ambiguous reporting hubs as unknown", func() {
		observe(clientWith(object(metav1.ConditionTrue)), clientWith(object(metav1.ConditionFalse)))
		c := check(unknown)
		Expect(c.GetReason()).To(Equal("StorageOnboardingStatusAmbiguous"))
	})
	It("does not claim completion when a hub cannot be observed", func() {
		broken := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, clnt.WithWatch, clnt.ObjectList, ...clnt.ListOption) error {
				return errors.New("private endpoint details")
			},
		}).Build()
		observe(clientWith(object(metav1.ConditionTrue)), broken)
		c := check(unknown)
		Expect(c.GetReason()).To(Equal("StorageOnboardingStatusUnknown"))
		Expect(c.GetMessage()).NotTo(ContainSubstring("private endpoint"))
	})
	It("reports a failed hub listing as unknown", func() {
		hubs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("unavailable"))
		check(unknown)
	})
	DescribeTable("does not copy another tenant's completion", func(mismatch string) {
		cr := object(metav1.ConditionTrue)
		want := pending
		switch mismatch {
		case "label":
			cr.Labels[labels.TenantID] = "foreign-id"
		case "namespace":
			cr.Namespace = "foreign-ns"
		case "name":
			cr.Name = "foreign-name"
			want = unknown
		}
		observe(clientWith(cr))
		check(want)
	}, Entry("different ID", "label"), Entry("different namespace", "namespace"), Entry("mismatched name", "name"))
	It("copies changed explanations and timestamps even when status stays True", func() {
		observe(clientWith(object(metav1.ConditionTrue)))
		check(complete)
		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		cr := object(metav1.ConditionTrue)
		cr.Status.Conditions[0].Reason = "OperatorUpdated"
		cr.Status.Conditions[0].Message = "Onboarding remains complete"
		cr.Status.Conditions[0].LastTransitionTime = metav1.NewTime(transition.Add(time.Hour))
		observe(clientWith(cr))
		c := check(complete)
		Expect(c.GetReason()).To(Equal("OperatorUpdated"))
		Expect(c.GetMessage()).To(Equal("Onboarding remains complete"))
		Expect(c.GetLastTransitionTime().AsTime()).To(Equal(transition.Add(time.Hour)))
	})
	It("does not independently latch a previous completion", func() {
		observe(clientWith(object(metav1.ConditionTrue)))
		check(complete)
		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		observe(clientWith(object(metav1.ConditionFalse)))
		check(pending)
	})
	It("preserves an absent operator transition timestamp", func() {
		cr := object(metav1.ConditionTrue)
		cr.Status.Conditions[0].LastTransitionTime = metav1.Time{}
		observe(clientWith(cr))
		Expect(check(complete).GetLastTransitionTime()).To(BeNil())
	})
	It("does not update an unchanged projection", func() {
		observe(clientWith(object(metav1.ConditionTrue)))
		check(complete)
		tenant = proto.Clone(tenants.updates[0].GetObject()).(*privatev1.Tenant)
		tenants.updates = nil
		observe(clientWith(object(metav1.ConditionTrue)))
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(tenants.updates).To(BeEmpty())
	})
	It("projects storage completion despite a lifecycle validation failure", func() {
		tenant.GetMetadata().SetTenant("")
		tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_UNSPECIFIED)
		observe(clientWith(object(metav1.ConditionTrue)))
		Expect(reconciler.Run(ctx, tenant)).To(MatchError(ContainSubstring("metadata.tenant")))
		Expect(tenants.updates).To(HaveLen(1))
		Expect(condition(tenants.updates[0].GetObject()).GetStatus()).To(Equal(complete))
		Expect(tenants.updates[0].GetUpdateMask().GetPaths()).To(ConsistOf("status.conditions"))
	})
})
