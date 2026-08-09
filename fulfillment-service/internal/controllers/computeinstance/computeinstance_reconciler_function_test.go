/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/gvks"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
)

var _ = Describe("buildSpec", func() {
	Describe("RestartRequestedAt field", func() {
		It("Includes restartRequestedAt in spec map when present", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			requestedAt := time.Date(2026, 1, 28, 13, 27, 0, 0, time.UTC)
			cpuCores, err := anypb.New(wrapperspb.String("2"))
			Expect(err).ToNot(HaveOccurred())
			memory, err := anypb.New(wrapperspb.String("4Gi"))
			Expect(err).ToNot(HaveOccurred())
			template := "osac.templates.ocp_virt_vm"

			// Set up fake client with subnet CR
			hubNamespace := "test-hub"
			subnetID := "test-subnet"
			subnetCR := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Name:      "test-sn",
					Labels:    map[string]string{labels.SubnetUuid: subnetID},
				},
			}
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(subnetCR).
				Build()

			mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
			mockInstanceTypesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.InstanceTypesGetResponse_builder{
					Object: privatev1.InstanceType_builder{
						Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-instance-123",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
						TemplateParameters: map[string]*anypb.Any{
							"cpu_cores": cpuCores,
							"memory":    memory,
						},
						RestartRequestedAt: timestamppb.New(requestedAt),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			// Call the actual buildSpec function
			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify restartRequestedAt was added with correct format
			Expect(spec.RestartRequestedAt).ToNot(BeNil())
			Expect(spec.RestartRequestedAt.Time).To(Equal(requestedAt))

			// Verify other required fields are present
			Expect(spec.TemplateID).To(Equal(template))
			Expect(spec.TemplateParameters).ToNot(BeEmpty())
		})

		It("Includes explicit fields in spec map when present", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			template := "osac.templates.ocp_virt_vm"

			// Set up fake client with subnet CR
			hubNamespace := "test-hub"
			subnetID := "test-subnet"
			subnetCR := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Name:      "test-sn",
					Labels:    map[string]string{labels.SubnetUuid: subnetID},
				},
			}
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(subnetCR).
				Build()

			mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
			mockInstanceTypesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.InstanceTypesGetResponse_builder{
					Object: privatev1.InstanceType_builder{
						Id: "standard-4-8",
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 8,
						}.Build(),
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-explicit-fields",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "standard-4-8"},
						RunStrategy:  new("Always"),
						SshPublicKey: new("ssh-rsa AAAA..."),
						Image: privatev1.ComputeInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/fedora/fedora:latest",
						}.Build(),
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib: 20,
						}.Build(),
						AdditionalDisks: []*privatev1.ComputeInstanceDisk{
							privatev1.ComputeInstanceDisk_builder{
								SizeGib: 100,
							}.Build(),
							privatev1.ComputeInstanceDisk_builder{
								SizeGib: 50,
							}.Build(),
						},
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				userDataSecretName: "test-explicit-fields-user-data",
				hubNamespace:       hubNamespace,
				hubClient:          fakeClient,
			}

			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(spec.Cores).To(Equal(int32(4)))
			Expect(spec.MemoryGiB).To(Equal(int32(8)))
			Expect(spec.RunStrategy).To(Equal(osacv1alpha1.RunStrategyType("Always")))
			Expect(spec.SSHKey).To(Equal("ssh-rsa AAAA..."))

			Expect(string(spec.Image.SourceType)).To(Equal("registry"))
			Expect(spec.Image.SourceRef).To(Equal("quay.io/fedora/fedora:latest"))

			Expect(spec.BootDisk.SizeGiB).To(Equal(int32(20)))

			Expect(spec.AdditionalDisks).To(HaveLen(2))
			Expect(spec.AdditionalDisks[0].SizeGiB).To(Equal(int32(100)))
			Expect(spec.AdditionalDisks[1].SizeGiB).To(Equal(int32(50)))

			Expect(spec.UserDataSecretRef).ToNot(BeNil())
			Expect(spec.UserDataSecretRef.Name).To(Equal("test-explicit-fields-user-data"))
		})

		Describe("Guest OS Family Mapping", func() {
			It("Maps is_windows=true to GuestOSFamily='windows'", func() {
				ctx := context.Background()
				ctrl := gomock.NewController(GinkgoT())
				DeferCleanup(ctrl.Finish)
				template := "osac.templates.ocp_virt_vm"

				// Set up fake client with subnet CR
				hubNamespace := "test-hub"
				subnetID := "test-subnet"
				subnetCR := &osacv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hubNamespace,
						Name:      "test-sn",
						Labels:    map[string]string{labels.SubnetUuid: subnetID},
					},
				}
				scheme := runtime.NewScheme()
				Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(corev1.AddToScheme(scheme)).To(Succeed())
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(subnetCR).
					Build()

				mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
				mockInstanceTypesClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(privatev1.InstanceTypesGetResponse_builder{
						Object: privatev1.InstanceType_builder{
							Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
						}.Build(),
					}.Build(), nil)

				// Create task with is_windows=true
				isWindows := true
				task := &task{
					r: &function{
						logger:              logger,
						instanceTypesClient: mockInstanceTypesClient,
					},
					computeInstance: privatev1.ComputeInstance_builder{
						Id: "test-windows-vm",
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
							InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
							IsWindows:    &isWindows,
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
							},
						}.Build(),
					}.Build(),
					hubNamespace: hubNamespace,
					hubClient:    fakeClient,
				}

				spec, err := task.buildSpec(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(spec.GuestOSFamily).To(Equal("windows"))
			})

			It("Maps is_windows=false to GuestOSFamily='linux'", func() {
				ctx := context.Background()
				ctrl := gomock.NewController(GinkgoT())
				DeferCleanup(ctrl.Finish)
				template := "osac.templates.ocp_virt_vm"

				// Set up fake client with subnet CR
				hubNamespace := "test-hub"
				subnetID := "test-subnet"
				subnetCR := &osacv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hubNamespace,
						Name:      "test-sn",
						Labels:    map[string]string{labels.SubnetUuid: subnetID},
					},
				}
				scheme := runtime.NewScheme()
				Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(corev1.AddToScheme(scheme)).To(Succeed())
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(subnetCR).
					Build()

				mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
				mockInstanceTypesClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(privatev1.InstanceTypesGetResponse_builder{
						Object: privatev1.InstanceType_builder{
							Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
						}.Build(),
					}.Build(), nil)

				// Create task with is_windows=false
				isWindows := false
				task := &task{
					r: &function{
						logger:              logger,
						instanceTypesClient: mockInstanceTypesClient,
					},
					computeInstance: privatev1.ComputeInstance_builder{
						Id: "test-linux-vm",
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
							InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
							IsWindows:    &isWindows,
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
							},
						}.Build(),
					}.Build(),
					hubNamespace: hubNamespace,
					hubClient:    fakeClient,
				}

				spec, err := task.buildSpec(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(spec.GuestOSFamily).To(Equal("linux"))
			})

			It("Maps is_windows not set (omitted) to GuestOSFamily='linux'", func() {
				ctx := context.Background()
				ctrl := gomock.NewController(GinkgoT())
				DeferCleanup(ctrl.Finish)
				template := "osac.templates.ocp_virt_vm"

				// Set up fake client with subnet CR
				hubNamespace := "test-hub"
				subnetID := "test-subnet"
				subnetCR := &osacv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hubNamespace,
						Name:      "test-sn",
						Labels:    map[string]string{labels.SubnetUuid: subnetID},
					},
				}
				scheme := runtime.NewScheme()
				Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(corev1.AddToScheme(scheme)).To(Succeed())
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(subnetCR).
					Build()

				mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
				mockInstanceTypesClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(privatev1.InstanceTypesGetResponse_builder{
						Object: privatev1.InstanceType_builder{
							Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
						}.Build(),
					}.Build(), nil)

				// Create task WITHOUT is_windows field (omitted entirely)
				task := &task{
					r: &function{
						logger:              logger,
						instanceTypesClient: mockInstanceTypesClient,
					},
					computeInstance: privatev1.ComputeInstance_builder{
						Id: "test-default-linux-vm",
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
							InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
							// IsWindows is NOT set - omitted
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
							},
						}.Build(),
					}.Build(),
					hubNamespace: hubNamespace,
					hubClient:    fakeClient,
				}

				spec, err := task.buildSpec(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(spec.GuestOSFamily).To(Equal("linux"))
			})
		})

		It("Excludes explicit fields from spec map when not set", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			template := "osac.templates.ocp_virt_vm"

			// Set up fake client with subnet CR
			hubNamespace := "test-hub"
			subnetID := "test-subnet"
			subnetCR := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Name:      "test-sn",
					Labels:    map[string]string{labels.SubnetUuid: subnetID},
				},
			}
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(subnetCR).
				Build()

			mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
			mockInstanceTypesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.InstanceTypesGetResponse_builder{
					Object: privatev1.InstanceType_builder{
						Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-no-explicit-fields",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(spec.Cores).To(BeZero())
			Expect(spec.MemoryGiB).To(BeZero())
			Expect(spec.RunStrategy).To(BeEmpty())
			Expect(spec.SSHKey).To(BeEmpty())
			Expect(spec.Image).To(Equal(osacv1alpha1.ImageSpec{}))
			Expect(spec.BootDisk).To(Equal(osacv1alpha1.DiskSpec{}))
			Expect(spec.AdditionalDisks).To(BeEmpty())
			Expect(spec.UserDataSecretRef).To(BeNil())
		})

		It("Excludes restartRequestedAt from spec map when not set", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			cpuCores, err := anypb.New(wrapperspb.String("1"))
			Expect(err).ToNot(HaveOccurred())
			memory, err := anypb.New(wrapperspb.String("2Gi"))
			Expect(err).ToNot(HaveOccurred())
			template := "osac.templates.ocp_virt_vm"

			// Set up fake client with subnet CR
			hubNamespace := "test-hub"
			subnetID := "test-subnet"
			subnetCR := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Name:      "test-sn",
					Labels:    map[string]string{labels.SubnetUuid: subnetID},
				},
			}
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(subnetCR).
				Build()

			mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
			mockInstanceTypesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.InstanceTypesGetResponse_builder{
					Object: privatev1.InstanceType_builder{
						Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-instance-456",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
						TemplateParameters: map[string]*anypb.Any{
							"cpu_cores": cpuCores,
							"memory":    memory,
						},
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
						// No RestartRequestedAt set
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			// Call the actual buildSpec function
			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify restartRequestedAt was NOT added
			Expect(spec.RestartRequestedAt).To(BeNil())

			// Verify other required fields are present
			Expect(spec.TemplateID).To(Equal(template))
			Expect(spec.TemplateParameters).ToNot(BeEmpty())
		})
	})
})

// newComputeInstanceCR creates a typed ComputeInstance CR for use with the fake client.
func newComputeInstanceCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.ComputeInstance {
	obj := &osacv1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.ComputeInstanceUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/computeinstance"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the compute instance.
func hasFinalizer(ci *privatev1.ComputeInstance) bool {
	return slices.Contains(ci.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(ciID, hubID string, hubCache controllers.HubCache) *task {
	ci := privatev1.ComputeInstance_builder{
		Id: ciID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:               f,
		computeInstance: ci,
	}
}

var _ = Describe("delete", func() {
	const (
		ciID         = "test-ci-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "vm-test"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should remove finalizer when K8s object doesn't exist", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(ciID, hubID, hubCache)
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newComputeInstanceCR(ciID, hubNamespace, crName, nil)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(ciID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		// Finalizer should NOT be removed — K8s object still exists
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newComputeInstanceCR(ciID, hubNamespace, crName, &now)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(ciID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		// Finalizer should NOT be removed — K8s object still being deleted
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(ciID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		// Finalizer should NOT be removed on error
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		// This test verifies the core behavior: when a hub is decommissioned/deleted,
		// the reconciler removes its finalizer to allow the compute instance to be archived.
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(ciID, hubID, hubCache)
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())

		err := t.delete(ctx)
		// Should return nil (not propagate the error)
		Expect(err).ToNot(HaveOccurred())
		// Finalizer should be removed to allow archiving
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
	})

	It("should remove finalizer when no hub is assigned", func() {
		ci := privatev1.ComputeInstance_builder{
			Id: ciID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				// No hub assigned
			}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:               f,
			computeInstance: ci,
		}

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
	})
})

var _ = Describe("getSubnetCR", func() {
	const (
		ciID         = "test-ci-subnet"
		subnetID     = "subnet-abc-123"
		hubNamespace = "test-ns"
		subnetCRName = "subnet-xyz"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should return Subnet CR when one exists with matching label", func() {
		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      subnetCRName,
				Labels: map[string]string{
					labels.SubnetUuid: subnetID,
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR).
			Build()

		t := &task{
			r:            &function{logger: logger},
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		result, err := t.getSubnetCR(ctx, subnetID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.GetName()).To(Equal(subnetCRName))
	})

	It("should return nil when no Subnet CR exists", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r:            &function{logger: logger},
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		result, err := t.getSubnetCR(ctx, subnetID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("should return error when multiple Subnet CRs match", func() {
		subnetCR1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "subnet-1",
				Labels: map[string]string{
					labels.SubnetUuid: subnetID,
				},
			},
		}

		subnetCR2 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "subnet-2",
				Labels: map[string]string{
					labels.SubnetUuid: subnetID,
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR1, subnetCR2).
			Build()

		t := &task{
			r:            &function{logger: logger},
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		result, err := t.getSubnetCR(ctx, subnetID)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected at most one subnet"))
		Expect(result).To(BeNil())
	})
})

var _ = Describe("getSecurityGroupCR", func() {
	const (
		hubNamespace = "test-ns"
		sgID         = "sg-abc-123"
	)

	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should return SecurityGroup CR when one exists with matching label", func() {
		sgCR := &osacv1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sg-cr-name",
				Labels: map[string]string{
					labels.SecurityGroupUuid: sgID,
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sgCR).
			Build()

		t := &task{
			r:            &function{logger: logger},
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		result, err := t.getSecurityGroupCR(ctx, sgID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.GetName()).To(Equal("sg-cr-name"))
	})

	It("should return nil when no SecurityGroup CR exists", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r:            &function{logger: logger},
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		result, err := t.getSecurityGroupCR(ctx, sgID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeNil())
	})
})

var _ = Describe("buildSpec with subnetRef", func() {
	const (
		hubNamespace = "test-ns"
		subnetID     = "subnet-abc-123"
		subnetCRName = "subnet-xyz"
	)

	var (
		ctx                     context.Context
		mockCtrl                *gomock.Controller
		mockInstanceTypesClient *MockInstanceTypesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockCtrl = gomock.NewController(GinkgoT())
		DeferCleanup(mockCtrl.Finish)
		mockInstanceTypesClient = NewMockInstanceTypesClient(mockCtrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
				}.Build(),
			}.Build(), nil).
			AnyTimes()
	})

	// Legacy subnet test cases removed - these fields are no longer supported

	It("should not set subnetRef when no subnet field", func() {
		subnetID := "test-subnet"
		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "test-sn",
				Labels:    map[string]string{labels.SubnetUuid: subnetID},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR).
			Build()

		template := "osac.templates.ocp_virt_vm"
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.NetworkAttachments).To(HaveLen(1))
		Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("test-sn"))
	})

	It("should populate two networkAttachments and omit top-level subnetRef for multi-NIC", func() {
		sid1, sid2 := "subnet-id-1", "subnet-id-2"
		subnetCR1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-1",
				Labels:    map[string]string{labels.SubnetUuid: sid1},
			},
		}

		subnetCR2 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-2",
				Labels:    map[string]string{labels.SubnetUuid: sid2},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR1, subnetCR2).
			Build()

		template := "osac.templates.ocp_virt_vm"
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: sid1}}.Build(),
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: sid2}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.NetworkAttachments).To(HaveLen(2))
		Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("sn-1"))
		Expect(spec.NetworkAttachments[1].SubnetRef).To(Equal("sn-2"))
	})

	It("should resolve securityGroupRefs inside networkAttachments", func() {
		sid, sgid := "subnet-id-1", "sg-id-1"
		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-1",
				Labels:    map[string]string{labels.SubnetUuid: sid},
			},
		}

		sgCR := &osacv1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sg-cr-1",
				Labels:    map[string]string{labels.SecurityGroupUuid: sgid},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR, sgCR).
			Build()

		template := "osac.templates.ocp_virt_vm"
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{
							Subnet:         &privatev1.SubnetLocalReference{Id: sid},
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{{Id: sgid}},
						}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.NetworkAttachments).To(HaveLen(1))
		Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("sn-1"))
		Expect(spec.NetworkAttachments[0].SecurityGroupRefs).To(Equal([]string{"sg-cr-1"}))
	})

	It("should return error when hubClient.List fails for SecurityGroup lookup", func() {
		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-1",
				Labels:    map[string]string{labels.SubnetUuid: "subnet-id"},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		// Create a fake client that will fail on List for SecurityGroup
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client clnt.WithWatch, list clnt.ObjectList, opts ...clnt.ListOption) error {
					// Fail only for SecurityGroup lists
					if _, ok := list.(*osacv1alpha1.SecurityGroupList); ok {
						return fmt.Errorf("simulated List error")
					}
					return client.List(ctx, list, opts...)
				},
			}).
			Build()

		template := "osac.templates.ocp_virt_vm"
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{
							Subnet:         &privatev1.SubnetLocalReference{Id: "subnet-id"},
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{{Id: "sg-id"}},
						}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		_, err := t.buildSpec(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to look up SecurityGroup CR"))
		Expect(err.Error()).To(ContainSubstring("simulated List error"))
	})

	It("should return error when subnet CR exists but SecurityGroup CR not found in network_attachments", func() {
		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-1",
				Labels:    map[string]string{labels.SubnetUuid: "subnet-id"},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR).
			Build()

		template := "osac.templates.ocp_virt_vm"
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{
							Subnet:         &privatev1.SubnetLocalReference{Id: "subnet-id"},
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{{Id: "missing-sg"}},
						}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		_, err := t.buildSpec(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("SecurityGroup CR not found"))
		Expect(err.Error()).To(ContainSubstring("missing-sg"))
	})
})

var _ = Describe("ensureUserDataSecret", func() {
	const (
		ciID         = "test-ci-user-data"
		hubNamespace = "test-ns"
		crName       = "vm-test"
		crUID        = "test-uid-123"
	)

	var (
		ctx   context.Context
		owner *osacv1alpha1.ComputeInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		owner = &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      crName,
				UID:       crUID,
			},
		}
	})

	It("should create a Secret with owner reference, labels, and content", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r: &function{logger: logger},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: ciID,
				Spec: privatev1.ComputeInstanceSpec_builder{
					UserData: new("#cloud-config\npackages:\n  - vim"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: ciID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())

		secret := &unstructured.Unstructured{}
		secret.SetGroupVersionKind(gvks.Secret)
		err = fakeClient.Get(ctx, clnt.ObjectKey{
			Namespace: hubNamespace,
			Name:      ciID + userDataSecretSuffix,
		}, secret)
		Expect(err).ToNot(HaveOccurred())

		stringData, _, _ := unstructured.NestedMap(secret.Object, "stringData")
		Expect(stringData[userDataSecretKey]).To(Equal("#cloud-config\npackages:\n  - vim"))

		Expect(secret.GetLabels()[labels.ComputeInstanceUuid]).To(Equal(ciID))

		ownerRefs := secret.GetOwnerReferences()
		Expect(ownerRefs).To(HaveLen(1))
		Expect(ownerRefs[0].Name).To(Equal(crName))
		Expect(ownerRefs[0].UID).To(Equal(owner.GetUID()))
		Expect(ownerRefs[0].Kind).To(Equal("ComputeInstance"))
	})

	It("should be idempotent when Secret already exists", func() {
		existingSecret := &unstructured.Unstructured{}
		existingSecret.SetGroupVersionKind(gvks.Secret)
		existingSecret.SetNamespace(hubNamespace)
		existingSecret.SetName(ciID + userDataSecretSuffix)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingSecret).
			Build()

		t := &task{
			r: &function{logger: logger},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: ciID,
				Spec: privatev1.ComputeInstanceSpec_builder{
					UserData: new("some-data"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: ciID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should propagate error when Secret creation fails", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return errors.New("create failed")
				},
			}).
			Build()

		t := &task{
			r: &function{logger: logger},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: ciID,
				Spec: privatev1.ComputeInstanceSpec_builder{
					UserData: new("some-data"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: ciID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("create failed"))
	})

	It("should not create a Secret when userDataSecretName is empty", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r: &function{logger: logger},
			computeInstance: privatev1.ComputeInstance_builder{
				Id:   ciID,
				Spec: privatev1.ComputeInstanceSpec_builder{}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("setReconciliationFailed", func() {
	It("should set state to FAILED and update PROVISIONED condition", func() {
		ci := privatev1.ComputeInstance_builder{
			Id: "test-ci-fail",
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
			}.Build(),
		}.Build()

		t := &task{
			r:               &function{logger: logger},
			computeInstance: ci,
		}

		reconcileErr := errors.New("spec.runStrategy: Unsupported value: \"\": supported values: \"Always\", \"Halted\"")
		t.setReconciliationFailed(reconcileErr)

		Expect(ci.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		Expect(ci.GetStatus().GetStateTransitionTime()).ToNot(BeNil())
		Expect(ci.GetStatus().GetStateTransitionTime().AsTime()).To(BeTemporally("~", time.Now(), time.Second))

		var provisionedCondition *privatev1.ComputeInstanceCondition
		for _, c := range ci.GetStatus().GetConditions() {
			if c.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
				provisionedCondition = c
				break
			}
		}
		Expect(provisionedCondition).ToNot(BeNil())
		Expect(provisionedCondition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(provisionedCondition.GetReason()).To(Equal("ReconciliationFailed"))
		Expect(provisionedCondition.GetMessage()).To(ContainSubstring("runStrategy"))
	})

	It("should create status if not present", func() {
		ci := privatev1.ComputeInstance_builder{
			Id: "test-ci-no-status",
		}.Build()

		t := &task{
			r:               &function{logger: logger},
			computeInstance: ci,
		}

		t.setReconciliationFailed(errors.New("hub not found"))

		Expect(ci.HasStatus()).To(BeTrue())
		Expect(ci.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		Expect(ci.GetStatus().GetStateTransitionTime()).ToNot(BeNil())
	})

	It("should preserve state_transition_time on repeated failures", func() {
		originalTime := timestamppb.New(time.Now().Add(-10 * time.Minute))
		ci := privatev1.ComputeInstance_builder{
			Id: "test-ci-repeated-fail",
			Status: privatev1.ComputeInstanceStatus_builder{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED,
				StateTransitionTime: originalTime,
			}.Build(),
		}.Build()

		t := &task{
			r:               &function{logger: logger},
			computeInstance: ci,
		}

		t.setReconciliationFailed(errors.New("still failing"))

		Expect(ci.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		Expect(ci.GetStatus().GetStateTransitionTime().AsTime()).To(Equal(originalTime.AsTime()))
	})

	It("should backfill state_transition_time when FAILED status has nil timestamp", func() {
		ci := privatev1.ComputeInstance_builder{
			Id: "test-ci-failed-nil-timestamp",
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED,
			}.Build(),
		}.Build()

		t := &task{
			r:               &function{logger: logger},
			computeInstance: ci,
		}

		t.setReconciliationFailed(errors.New("failing again"))

		Expect(ci.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		Expect(ci.GetStatus().GetStateTransitionTime()).ToNot(BeNil())
		Expect(ci.GetStatus().GetStateTransitionTime().AsTime()).To(BeTemporally("~", time.Now(), time.Second))
	})

	It("should update existing PROVISIONED condition rather than creating duplicate", func() {
		reason := "WaitingForVM"
		message := "initial state"
		ci := privatev1.ComputeInstance_builder{
			Id: "test-ci-existing-condition",
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Conditions: []*privatev1.ComputeInstanceCondition{
					privatev1.ComputeInstanceCondition_builder{
						Type:    privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
						Status:  privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
						Reason:  &reason,
						Message: &message,
					}.Build(),
				},
			}.Build(),
		}.Build()

		t := &task{
			r:               &function{logger: logger},
			computeInstance: ci,
		}

		t.setReconciliationFailed(errors.New("create failed"))

		conditions := ci.GetStatus().GetConditions()
		provisionedCount := 0
		for _, c := range conditions {
			if c.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
				provisionedCount++
				Expect(c.GetReason()).To(Equal("ReconciliationFailed"))
				Expect(c.GetMessage()).To(ContainSubstring("create failed"))
			}
		}
		Expect(provisionedCount).To(Equal(1))
	})
})

var _ = Describe("hub persistence", func() {
	const (
		computeInstanceID = "test-ci-hub"
		tenantName        = "test-tenant"
		hubID             = "test-hub-123"
		hubNamespace      = "hub-123-ns"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should select hub and return without creating ComputeInstance VM", func() {

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: hubID}.Build(),
				},
			}, nil)

		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		computeInstance := privatev1.ComputeInstance_builder{
			Id: computeInstanceID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			maskCalculator:         nil,
		}

		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify hub was set in status
		Expect(computeInstance.GetStatus().GetHub()).To(Equal(hubID))

		// Verify ComputeInstance CR was NOT created (early return)
		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should not create CR when no hubs available", func() {

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)

		// Mock the hubs list returning empty — no hubs available
		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{},
			}, nil)

		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		computeInstance := privatev1.ComputeInstance_builder{
			Id: computeInstanceID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   "", // Empty - needs hub selection
			}.Build(),
		}.Build()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			maskCalculator:         nil,
		}

		err := f.run(ctx, computeInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("there are no hubs"))

		// Verify status was set to FAILED with error details
		Expect(computeInstance.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))

		// Verify ComputeInstance was NOT created
		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty(), "ComputeInstance should NOT be created when no hubs available")
	})

	It("should skip hub selection if already set", func() {

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		// Hub selection should NOT be called (status.hub already set)
		// No call to hubsClient.List expected
		hubsClient := controllers.NewMockHubsClient(ctrl)

		// Only expect final update (no hub persistence update)
		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				// Verify status.hub is NOT in the field mask (already set, no update needed)
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
				}.Build(),
			}.Build(), nil).
			AnyTimes()

		computeInstance := privatev1.ComputeInstance_builder{
			Id: computeInstanceID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   hubID, // Hub already set
			}.Build(),
		}.Build()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}

		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify ComputeInstance was created on the existing hub
		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})

	It("should create CR on second reconcile after hub is persisted", func() {

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		// First reconcile: select random hub
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: hubID}.Build(),
				},
			}, nil)

		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
				}.Build(),
			}.Build(), nil).
			AnyTimes()

		computeInstance := privatev1.ComputeInstance_builder{
			Id: computeInstanceID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   "", // Empty initially
			}.Build(),
		}.Build()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}

		// First reconcile: hub is empty, selects hub and returns early — no CR
		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())

		// Second reconcile: hub already set, should create the CR
		computeInstance.GetStatus().SetHub(hubID)

		err = f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		// CR should now exist
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})
})

var _ = Describe("instance_type resolution in reconciler", func() {
	const (
		hubNamespace = "test-ns"
		subnetID     = "test-subnet"
	)

	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		fakeClient clnt.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		subnetCR := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "test-sn",
				Labels:    map[string]string{labels.SubnetUuid: subnetID},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR).
			Build()
	})

	It("resolves instance_type to cores/memory_gib on CR spec", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "test-type",
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 8,
					}.Build(),
				}.Build(),
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance-it",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.Cores).To(Equal(int32(4)))
		Expect(spec.MemoryGiB).To(Equal(int32(8)))
	})

	It("returns error when instance_type is empty", func() {
		t := &task{
			r: &function{
				logger: logger,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-empty-it",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template: &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		_, err := t.buildSpec(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no instance_type set"))
	})

	It("sets osac.io/instance-type-name label on CR when instance_type is set", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "test-type",
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 8,
					}.Build(),
				}.Build(),
			}.Build(), nil)

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "test-hub").
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)

		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		computeInstance := privatev1.ComputeInstance_builder{
			Id: "test-instance-label",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
				InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
				NetworkAttachments: []*privatev1.NetworkAttachment{
					privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
				},
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   "test-hub",
			}.Build(),
		}.Build()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}

		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify the CR was created with the label
		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Labels).To(HaveKeyWithValue(labels.InstanceTypeName, "test-type"))
	})

	It("returns error when InstanceType lookup fails (triggers requeue)", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("connection refused"))

		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-instance-fail",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: "failing-type"},
					NetworkAttachments: []*privatev1.NetworkAttachment{
						privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		_, err := t.buildSpec(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to resolve instance type 'failing-type'"))
		Expect(err.Error()).To(ContainSubstring("connection refused"))
	})

})

var _ = Describe("Kubernetes validation error handling", func() {
	const (
		computeInstanceID = "test-ci-validation"
		tenantName        = "test-tenant"
		hubID             = "test-hub"
		hubNamespace      = "test-ns"
		subnetID          = "test-subnet"
	)

	var (
		ctx                     context.Context
		ctrl                    *gomock.Controller
		scheme                  *runtime.Scheme
		subnetCR                *osacv1alpha1.Subnet
		hubsClient              *controllers.MockHubsClient
		mockInstanceTypesClient *MockInstanceTypesClient
		computeInstancesClient  *MockComputeInstancesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		subnetCR = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "test-sn",
				Labels:    map[string]string{labels.SubnetUuid: subnetID},
			},
		}

		hubsClient = controllers.NewMockHubsClient(ctrl)
		mockInstanceTypesClient = NewMockInstanceTypesClient(ctrl)

		computeInstancesClient = NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)
	})

	newComputeInstance := func() *privatev1.ComputeInstance {
		return privatev1.ComputeInstance_builder{
			Id: computeInstanceID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
				InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
				NetworkAttachments: []*privatev1.NetworkAttachment{
					privatev1.NetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
				},
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:   hubID,
			}.Build(),
		}.Build()
	}

	newTestHarness := func(interceptorFuncs interceptor.Funcs, extraObjects ...clnt.Object) (*function, clnt.Client) {
		objects := append([]clnt.Object{subnetCR}, extraObjects...)
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithInterceptorFuncs(interceptorFuncs).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}
		return f, fakeClient
	}

	It("should set state to FAILED when K8s Create returns Invalid error", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     150,
						MemoryGib: 2,
					}.Build(),
				}.Build(),
			}.Build(), nil)

		f, _ := newTestHarness(interceptor.Funcs{
			Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
				if _, ok := obj.(*osacv1alpha1.ComputeInstance); ok {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ComputeInstance"},
						"vm-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "cores"),
								150,
								"spec.cores in body should be less than or equal to 128",
							),
						},
					)
				}
				return client.Create(ctx, obj, opts...)
			},
		})

		computeInstance := newComputeInstance()
		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		Expect(computeInstance.GetStatus().GetState()).To(
			Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED),
		)

		conditions := computeInstance.GetStatus().GetConditions()
		var configCondition *privatev1.ComputeInstanceCondition
		for _, c := range conditions {
			if c.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED {
				configCondition = c
				break
			}
		}
		Expect(configCondition).ToNot(BeNil())
		Expect(configCondition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(configCondition.GetReason()).To(Equal("ValidationFailed"))
		Expect(configCondition.GetMessage()).To(ContainSubstring("spec.cores"))
		Expect(configCondition.GetMessage()).To(ContainSubstring("128"))
	})

	It("should set state to FAILED when K8s Patch returns Invalid error", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     200,
						MemoryGib: 2,
					}.Build(),
				}.Build(),
			}.Build(), nil)

		existingCR := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "vm-existing",
				Labels: map[string]string{
					labels.ComputeInstanceUuid: computeInstanceID,
				},
			},
		}

		f, _ := newTestHarness(interceptor.Funcs{
			Patch: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, patch clnt.Patch, opts ...clnt.PatchOption) error {
				if _, ok := obj.(*osacv1alpha1.ComputeInstance); ok {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ComputeInstance"},
						"vm-existing",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "cores"),
								200,
								"spec.cores in body should be less than or equal to 128",
							),
						},
					)
				}
				return client.Patch(ctx, obj, patch, opts...)
			},
		}, existingCR)

		computeInstance := newComputeInstance()
		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		Expect(computeInstance.GetStatus().GetState()).To(
			Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED),
		)
	})

	It("should still return transient errors from K8s Create", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{}.Build(),
				}.Build(),
			}.Build(), nil)

		f, _ := newTestHarness(interceptor.Funcs{
			Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
				if _, ok := obj.(*osacv1alpha1.ComputeInstance); ok {
					return errors.New("connection refused")
				}
				return client.Create(ctx, obj, opts...)
			},
		})

		computeInstance := newComputeInstance()
		err := f.run(ctx, computeInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))

		Expect(computeInstance.GetStatus().GetState()).To(
			Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING),
		)
	})

	It("should recover from FAILED state when spec is corrected and Create succeeds", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 2,
					}.Build(),
				}.Build(),
			}.Build(), nil).
			AnyTimes()

		rejectCreate := true
		f, fakeClient := newTestHarness(interceptor.Funcs{
			Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
				if _, ok := obj.(*osacv1alpha1.ComputeInstance); ok && rejectCreate {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ComputeInstance"},
						"vm-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "cores"),
								150,
								"spec.cores in body should be less than or equal to 128",
							),
						},
					)
				}
				return client.Create(ctx, obj, opts...)
			},
		})

		computeInstance := newComputeInstance()

		// First reconcile: K8s rejects Create with Invalid error → FAILED
		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(computeInstance.GetStatus().GetState()).To(
			Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED),
		)

		// Second reconcile: spec corrected, Create succeeds → CR created
		rejectCreate = false
		err = f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Namespace).To(Equal(hubNamespace))
	})
})
