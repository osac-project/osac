/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type mockTenantsClient struct {
	signalIDs   []string
	signalError error
}

func (m *mockTenantsClient) List(context.Context, *privatev1.TenantsListRequest, ...grpc.CallOption) (*privatev1.TenantsListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTenantsClient) Get(context.Context, *privatev1.TenantsGetRequest, ...grpc.CallOption) (*privatev1.TenantsGetResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTenantsClient) Create(context.Context, *privatev1.TenantsCreateRequest, ...grpc.CallOption) (*privatev1.TenantsCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTenantsClient) Delete(context.Context, *privatev1.TenantsDeleteRequest, ...grpc.CallOption) (*privatev1.TenantsDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTenantsClient) Update(context.Context, *privatev1.TenantsUpdateRequest, ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTenantsClient) Signal(_ context.Context, request *privatev1.TenantsSignalRequest, _ ...grpc.CallOption) (*privatev1.TenantsSignalResponse, error) {
	m.signalIDs = append(m.signalIDs, request.GetId())
	if m.signalError != nil {
		return nil, m.signalError
	}
	return &privatev1.TenantsSignalResponse{}, nil
}

var _ = Describe("tenantStatusChangedPredicate", func() {
	predicate := tenantStatusChangedPredicate()

	It("passes create and delete events", func() {
		object := &v1alpha1.Tenant{}
		Expect(predicate.Create(event.CreateEvent{Object: object})).To(BeTrue())
		Expect(predicate.Delete(event.DeleteEvent{Object: object})).To(BeTrue())
	})

	It("passes status changes", func() {
		oldObject := &v1alpha1.Tenant{Status: v1alpha1.TenantStatus{Phase: v1alpha1.TenantPhaseProgressing}}
		newObject := oldObject.DeepCopy()
		newObject.Status.Phase = v1alpha1.TenantPhaseReady

		Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldObject, ObjectNew: newObject})).To(BeTrue())
	})

	It("passes deletion timestamp changes", func() {
		oldObject := &v1alpha1.Tenant{}
		newObject := oldObject.DeepCopy()
		now := metav1.Now()
		newObject.DeletionTimestamp = &now

		Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldObject, ObjectNew: newObject})).To(BeTrue())
	})

	It("filters metadata-only updates", func() {
		oldObject := &v1alpha1.Tenant{Status: v1alpha1.TenantStatus{Phase: v1alpha1.TenantPhaseReady}}
		newObject := oldObject.DeepCopy()
		newObject.Annotations = map[string]string{"example.com/change": "metadata-only"}

		Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldObject, ObjectNew: newObject})).To(BeFalse())
	})
})

var _ = Describe("TenantFeedbackReconciler", func() {
	const (
		tenantName      = "tenant-a"
		tenantID        = "d73a5fa1-2869-421c-93df-cbe826053812"
		tenantNamespace = "osac"
	)

	var (
		ctx           context.Context
		k8sClient     client.Client
		tenantsClient *mockTenantsClient
		reconciler    *TenantFeedbackReconciler
		request       reconcile.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		tenantsClient = &mockTenantsClient{}
		reconciler = &TenantFeedbackReconciler{
			hubClient:       k8sClient,
			tenantsClient:   tenantsClient,
			tenantNamespace: tenantNamespace,
		}
		request = reconcile.Request{NamespacedName: types.NamespacedName{Name: tenantName, Namespace: tenantNamespace}}
	})

	It("ignores a missing Tenant CR", func() {
		result, err := reconciler.Reconcile(ctx, request)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())
		Expect(tenantsClient.signalIDs).To(BeEmpty())
	})

	It("ignores a Tenant CR without a fulfillment identifier", func() {
		object := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: tenantNamespace}}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, request)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())
		Expect(tenantsClient.signalIDs).To(BeEmpty())
	})

	It("signals the fulfillment tenant identified by the CR label", func() {
		object := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
			Name: tenantName, Namespace: tenantNamespace,
			Labels: map[string]string{osacTenantIDLabel: tenantID},
		}}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, request)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())
		Expect(tenantsClient.signalIDs).To(Equal([]string{tenantID}))
		updated := &v1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, updated)).To(Succeed())
		Expect(updated.Finalizers).To(BeEmpty())
	})

	It("returns signal failures so controller-runtime retries", func() {
		object := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
			Name: tenantName, Namespace: tenantNamespace,
			Labels: map[string]string{osacTenantIDLabel: tenantID},
		}}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())
		tenantsClient.signalError = errors.New("fulfillment unavailable")

		result, err := reconciler.Reconcile(ctx, request)

		Expect(err).To(MatchError("fulfillment unavailable"))
		Expect(result.IsZero()).To(BeTrue())
		Expect(tenantsClient.signalIDs).To(Equal([]string{tenantID}))
	})
})
