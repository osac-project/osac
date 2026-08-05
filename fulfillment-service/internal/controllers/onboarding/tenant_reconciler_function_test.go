/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package onboarding

import (
	"context"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

func hasFinalizer(tenant *privatev1.Tenant) bool {
	return slices.Contains(tenant.GetMetadata().GetFinalizers(), finalizers.Controller)
}

func newTenantCR(tenantID, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.Tenant {
	obj := &osacv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.TenantUuid: tenantID,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/tenant"})
	}
	return obj
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

func newFunction(
	hubCache controllers.HubCache,
	hubsClient privatev1.HubsClient,
	tenantsClient privatev1.TenantsClient,
	projectsClient ...privatev1.ProjectsClient,
) *function {
	f := &function{
		logger:         logger,
		hubCache:       hubCache,
		hubsClient:     hubsClient,
		tenantsClient:  tenantsClient,
		maskCalculator: masks.NewCalculator().Build(),
	}
	if len(projectsClient) > 0 {
		f.projectsClient = projectsClient[0]
	}
	return f
}

var _ = Describe("addFinalizer", func() {
	It("adds finalizer when not present and creates metadata", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.tenant)).To(BeTrue())
	})

	It("adds finalizer when not present but metadata exists", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.tenant)).To(BeTrue())
		Expect(t.tenant.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("does not add finalizer when already present", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
				}.Build(),
			}.Build(),
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		finalizerList := t.tenant.GetMetadata().GetFinalizers()
		Expect(finalizerList).To(HaveLen(1))
		Expect(finalizerList[0]).To(Equal(finalizers.Controller))
	})
})

var _ = Describe("removeFinalizer", func() {
	It("removes the controller finalizer when present", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller, "other-finalizer"},
				}.Build(),
			}.Build(),
		}

		t.removeFinalizer()

		Expect(hasFinalizer(t.tenant)).To(BeFalse())
		Expect(t.tenant.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
	})

	It("does nothing when finalizer is not present", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"other-finalizer"},
				}.Build(),
			}.Build(),
		}

		t.removeFinalizer()

		Expect(t.tenant.GetMetadata().GetFinalizers()).To(ConsistOf("other-finalizer"))
	})

	It("does nothing when metadata is missing", func() {
		t := &task{
			tenant: privatev1.Tenant_builder{}.Build(),
		}

		t.removeFinalizer()

		Expect(t.tenant.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("run", func() {
	const (
		tenantID   = "tenant-123"
		tenantName = "my-tenant"
		hub1ID     = "hub-1"
		hub2ID     = "hub-2"
		namespace1 = "hub-1-ns"
		namespace2 = "hub-2-ns"
	)

	var (
		ctx          context.Context
		ctrl         *gomock.Controller
		mockHubCache *controllers.MockHubCache
		mockHubs     *controllers.MockHubsClient
		mockTenants  *MockTenantsClient
		mockProjects *MockProjectsClient
		scheme       *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockHubCache = controllers.NewMockHubCache(ctrl)
		mockHubs = controllers.NewMockHubsClient(ctrl)
		mockTenants = NewMockTenantsClient(ctrl)
		mockProjects = NewMockProjectsClient(ctrl)
		mockProjects.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			AnyTimes()
		scheme = newScheme()
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("update (create/update path)", func() {
		When("tenant has no finalizer", func() {
			It("adds finalizer and returns early", func() {
				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Tenant: tenantName,
					}.Build(),
				}.Build()

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})

		When("tenant is created and no Tenant CRDs exist", func() {
			It("creates Tenant CRD on each hub", func() {
				fakeClient1 := fake.NewClientBuilder().WithScheme(scheme).Build()
				fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  2,
						Total: 2,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
							privatev1.Hub_builder{Id: hub2ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient1}, nil)
				mockHubCache.EXPECT().
					Get(gomock.Any(), hub2ID).
					Return(&controllers.HubEntry{Namespace: namespace2, Client: fakeClient2}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				list1 := &osacv1alpha1.TenantList{}
				Expect(fakeClient1.List(ctx, list1)).To(Succeed())
				Expect(list1.Items).To(HaveLen(1))
				Expect(list1.Items[0].Labels[labels.TenantUuid]).To(Equal(tenantID))
				Expect(list1.Items[0].Namespace).To(Equal(namespace1))

				list2 := &osacv1alpha1.TenantList{}
				Expect(fakeClient2.List(ctx, list2)).To(Succeed())
				Expect(list2.Items).To(HaveLen(1))
				Expect(list2.Items[0].Labels[labels.TenantUuid]).To(Equal(tenantID))
				Expect(list2.Items[0].Namespace).To(Equal(namespace2))

				ns1 := &corev1.Namespace{}
				Expect(fakeClient1.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns1)).To(Succeed())
				Expect(ns1.Labels[labels.TenantRef]).To(Equal(tenantID))
				Expect(ns1.Labels[labels.Project]).To(Equal(namespace1))

				ns2 := &corev1.Namespace{}
				Expect(fakeClient2.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns2)).To(Succeed())
				Expect(ns2.Labels[labels.TenantRef]).To(Equal(tenantID))
				Expect(ns2.Labels[labels.Project]).To(Equal(namespace2))
			})
		})

		When("Tenant CRD already exists on a hub", func() {
			It("does not create a duplicate but still ensures namespace", func() {
				existing := newTenantCR(tenantID, namespace1, tenantID, nil)
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				list := &osacv1alpha1.TenantList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())
				Expect(list.Items).To(HaveLen(1))

				ns := &corev1.Namespace{}
				Expect(fakeClient.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns)).To(Succeed())
				Expect(ns.Labels[labels.TenantRef]).To(Equal(tenantID))
			})
		})

		When("Tenant CRD exists on hub with missing labels", func() {
			It("patches the labels onto the existing object", func() {
				existing := &osacv1alpha1.Tenant{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace1,
						Name:      tenantID,
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				patched := &osacv1alpha1.Tenant{}
				Expect(fakeClient.Get(ctx, clnt.ObjectKey{
					Namespace: namespace1,
					Name:      tenantID,
				}, patched)).To(Succeed())
				Expect(patched.Labels).To(HaveKeyWithValue(labels.TenantUuid, tenantID))
			})
		})

		When("tenant namespace already exists on a hub", func() {
			It("does not create a duplicate namespace", func() {
				existingNS := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: tenantID,
						Labels: map[string]string{
							labels.TenantRef: tenantID,
							labels.Project:   namespace1,
						},
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existingNS).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				nsList := &corev1.NamespaceList{}
				Expect(fakeClient.List(ctx, nsList)).To(Succeed())
				Expect(nsList.Items).To(HaveLen(1))
			})
		})

		When("tenant namespace exists on hub with stale labels", func() {
			It("patches the labels to match current state", func() {
				existingNS := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: tenantID,
						Labels: map[string]string{
							labels.TenantRef: "old-value",
							labels.Project:   "old-project",
						},
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existingNS).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				ns := &corev1.Namespace{}
				Expect(fakeClient.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns)).To(Succeed())
				Expect(ns.Labels[labels.TenantRef]).To(Equal(tenantID))
				Expect(ns.Labels[labels.Project]).To(Equal(namespace1))
			})
		})

		When("listing hubs fails", func() {
			It("returns the error", func() {
				expectedErr := errors.New("hubs unavailable")
				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(nil, expectedErr)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(MatchError(ContainSubstring("hubs unavailable")))
			})
		})

		When("hub cache returns transient error", func() {
			It("returns the error and does not skip the hub", func() {
				expectedErr := errors.New("cache temporarily unavailable")

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(nil, expectedErr)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(MatchError(ContainSubstring("cache temporarily unavailable")))
			})
		})

		When("hub cache returns ErrHubNotFound during update", func() {
			It("skips the decommissioned hub and continues", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  2,
						Total: 2,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
							privatev1.Hub_builder{Id: hub2ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(nil, controllers.ErrHubNotFound)
				mockHubCache.EXPECT().
					Get(gomock.Any(), hub2ID).
					Return(&controllers.HubEntry{Namespace: namespace2, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					}).AnyTimes()

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())

				list := &osacv1alpha1.TenantList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())
				Expect(list.Items).To(HaveLen(1))
				Expect(list.Items[0].Labels[labels.TenantUuid]).To(Equal(tenantID))

				ns := &corev1.Namespace{}
				Expect(fakeClient.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns)).To(Succeed())
				Expect(ns.Labels[labels.TenantRef]).To(Equal(tenantID))
			})
		})

		When("creating a Tenant CRD on a hub fails", func() {
			It("sets status to FAILED and returns the error for requeue", func() {
				expectedErr := errors.New("create failed")
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
							return expectedErr
						},
					}).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(HaveOccurred())
				Expect(tenant.GetStatus().GetState()).To(
					Equal(privatev1.TenantState_TENANT_STATE_FAILED),
				)
				Expect(tenant.GetStatus().GetMessage()).To(
					ContainSubstring(hub1ID),
				)
				Expect(tenant.GetStatus().GetMessage()).ToNot(
					ContainSubstring("create failed"),
				)
			})
		})

		When("first hub succeeds but second hub fails", func() {
			It("persists hub1 resources and sets FAILED for hub2", func() {
				fakeClient1 := fake.NewClientBuilder().WithScheme(scheme).Build()
				expectedErr := errors.New("hub2 unreachable")
				fakeClient2 := fake.NewClientBuilder().
					WithScheme(scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
							return expectedErr
						},
					}).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  2,
						Total: 2,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
							privatev1.Hub_builder{Id: hub2ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient1}, nil)
				mockHubCache.EXPECT().
					Get(gomock.Any(), hub2ID).
					Return(&controllers.HubEntry{Namespace: namespace2, Client: fakeClient2}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants)
				err := f.run(ctx, tenant)

				Expect(err).To(HaveOccurred())
				Expect(tenant.GetStatus().GetState()).To(
					Equal(privatev1.TenantState_TENANT_STATE_FAILED),
				)
				Expect(tenant.GetStatus().GetMessage()).To(ContainSubstring(hub2ID))

				list1 := &osacv1alpha1.TenantList{}
				Expect(fakeClient1.List(ctx, list1)).To(Succeed())
				Expect(list1.Items).To(HaveLen(1))

				ns1 := &corev1.Namespace{}
				Expect(fakeClient1.Get(ctx, clnt.ObjectKey{Name: tenantID}, ns1)).To(Succeed())
			})
		})

		When("tenant was previously FAILED (no IDP sync) and hub sync now succeeds", func() {
			It("clears the failed state back to PENDING", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
					Status: privatev1.TenantStatus_builder{
						State:   privatev1.TenantState_TENANT_STATE_FAILED,
						Message: new("previous failure"),
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(tenant.GetStatus().GetState()).To(
					Equal(privatev1.TenantState_TENANT_STATE_PENDING),
				)
				Expect(tenant.GetStatus().HasMessage()).To(BeFalse())
			})
		})

		When("tenant was previously FAILED (IDP already synced) and hub sync now succeeds", func() {
			It("restores state to SYNCED instead of PENDING", func() {
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				idpName := "my-idp-tenant"
				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:       tenantID,
						Finalizers: []string{finalizers.Controller},
						Tenant:     tenantName,
					}.Build(),
					Status: privatev1.TenantStatus_builder{
						State:         privatev1.TenantState_TENANT_STATE_FAILED,
						Message:       new("hub sync failure"),
						IdpTenantName: idpName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(tenant.GetStatus().GetState()).To(
					Equal(privatev1.TenantState_TENANT_STATE_SYNCED),
				)
				Expect(tenant.GetStatus().HasMessage()).To(BeFalse())
				Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal(idpName))
			})
		})
	})

	Describe("delete path", func() {
		When("tenant is deleted and Tenant CRD exists on a hub", func() {
			It("issues delete for tenant and namespace, keeps finalizer until object is gone", func() {
				existing := newTenantCR(tenantID, namespace1, tenantID, nil)
				existingNS := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: tenantID,
						Labels: map[string]string{
							labels.TenantRef: tenantID,
							labels.Project:   namespace1,
						},
					},
				}
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, existingNS).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})

		When("no Tenant CRDs exist on any hub", func() {
			It("removes finalizer immediately", func() {
				fakeClient1 := fake.NewClientBuilder().WithScheme(scheme).Build()
				fakeClient2 := fake.NewClientBuilder().WithScheme(scheme).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  2,
						Total: 2,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
							privatev1.Hub_builder{Id: hub2ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient1}, nil)
				mockHubCache.EXPECT().
					Get(gomock.Any(), hub2ID).
					Return(&controllers.HubEntry{Namespace: namespace2, Client: fakeClient2}, nil)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(hasFinalizer(tenant)).To(BeFalse())
			})
		})

		When("Tenant CRD still has a deletion timestamp (K8s finalizers processing)", func() {
			It("keeps the finalizer and waits", func() {
				now := metav1.Now()
				existing := newTenantCR(tenantID, namespace1, tenantID, &now)
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})

		When("hub cache returns ErrHubNotFound during delete", func() {
			It("removes finalizer for the decommissioned hub", func() {
				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(nil, controllers.ErrHubNotFound)

				mockTenants.EXPECT().
					Update(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (*privatev1.TenantsUpdateResponse, error) {
						return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
					})

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).ToNot(HaveOccurred())
				Expect(hasFinalizer(tenant)).To(BeFalse())
			})
		})

		When("hub cache returns transient error during delete", func() {
			It("returns the error and keeps finalizer", func() {
				expectedErr := errors.New("cache temporarily unavailable")

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(nil, expectedErr)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(MatchError(ContainSubstring("cache temporarily unavailable")))
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})

		When("listing hubs fails during delete", func() {
			It("returns the error", func() {
				expectedErr := errors.New("hubs unavailable")
				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(nil, expectedErr)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(MatchError(ContainSubstring("hubs unavailable")))
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})

		When("K8s Get operation fails during delete", func() {
			It("returns the error", func() {
				expectedErr := errors.New("get failed")
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, client clnt.WithWatch, key clnt.ObjectKey, obj clnt.Object, opts ...clnt.GetOption) error {
							return expectedErr
						},
					}).
					Build()

				mockHubs.EXPECT().
					List(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsListResponse{
						Size:  1,
						Total: 1,
						Items: []*privatev1.Hub{
							privatev1.Hub_builder{Id: hub1ID}.Build(),
						},
					}, nil)

				mockHubCache.EXPECT().
					Get(gomock.Any(), hub1ID).
					Return(&controllers.HubEntry{Namespace: namespace1, Client: fakeClient}, nil)

				tenant := privatev1.Tenant_builder{
					Id: tenantID,
					Metadata: privatev1.Metadata_builder{
						Name:              tenantID,
						Finalizers:        []string{finalizers.Controller},
						DeletionTimestamp: timestamppb.Now(),
						Tenant:            tenantName,
					}.Build(),
				}.Build()

				f := newFunction(mockHubCache, mockHubs, mockTenants, mockProjects)
				err := f.run(ctx, tenant)

				Expect(err).To(MatchError(expectedErr))
				Expect(hasFinalizer(tenant)).To(BeTrue())
			})
		})
	})
})
