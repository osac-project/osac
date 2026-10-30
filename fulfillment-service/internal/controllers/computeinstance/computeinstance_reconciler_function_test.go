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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/gvks"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const testSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

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
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
							Vcpus:     4,
							MemoryGib: 8,
						}.Build(),
					}.Build(),
				}.Build(), nil)

			mockDiskImagesClient := NewMockDiskImagesClient(ctrl)
			mockDiskImagesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.DiskImagesGetResponse_builder{
					Object: privatev1.DiskImage_builder{
						Id: "test-disk-image",
						Spec: privatev1.DiskImageSpec_builder{
							SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
							SourceRef:     "quay.io/osac/rhel9:latest",
							GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
						}.Build(),
					}.Build(),
				}.Build(), nil)

			mockSecretsClient := NewMockSecretsClient(ctrl)
			mockSecretsClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.SecretsGetResponse_builder{
					Object: privatev1.Secret_builder{
						Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
						Data: map[string][]byte{"public_key": []byte(testSSHPublicKey)},
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
					diskImagesClient:    mockDiskImagesClient,
					secretsClient:       mockSecretsClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-explicit-fields",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "standard-4-8"},
						RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						SshKey:       privatev1.SecretReference_builder{Id: "test-ssh-key", Name: "login"}.Build(),
						DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(20),
							StorageTier: privatev1.StorageTierReference_builder{Name: "fast"}.Build(),
						}.Build(),
						AdditionalDisks: privatev1.ComputeInstanceDiskList_builder{Items: []*privatev1.ComputeInstanceDisk{
							privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(50),
								StorageTier: privatev1.StorageTierReference_builder{Name: "archive"}.Build(),
							}.Build(),
						}}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				userDataSecretName: "test-explicit-fields-user-data",
				hubNamespace:       hubNamespace,
				hubClient:          fakeClient,
			}

			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(spec.VCPUs).To(Equal(int32(4)))
			Expect(spec.MemoryGiB).To(Equal(int32(8)))
			Expect(spec.RunStrategy).To(Equal(osacv1alpha1.RunStrategyType("Always")))
			Expect(spec.SSHKey).To(Equal(testSSHPublicKey))

			Expect(spec.Image.SourceType).To(Equal(osacv1alpha1.ImageSourceTypeRegistry))
			Expect(spec.Image.SourceRef).To(Equal("quay.io/osac/rhel9:latest"))
			Expect(spec.GuestOSFamily).To(Equal("linux"))

			Expect(spec.BootDisk.SizeGiB).To(Equal(int32(20)))
			Expect(spec.BootDisk.StorageTier).To(Equal("fast"))

			Expect(spec.AdditionalDisks).To(HaveLen(2))
			Expect(spec.AdditionalDisks[0].SizeGiB).To(Equal(int32(100)))
			Expect(spec.AdditionalDisks[0].StorageTier).To(Equal("standard"))
			Expect(spec.AdditionalDisks[1].SizeGiB).To(Equal(int32(50)))
			Expect(spec.AdditionalDisks[1].StorageTier).To(Equal("archive"))

			Expect(spec.UserDataSecretRef).ToNot(BeNil())
			Expect(spec.UserDataSecretRef.Name).To(Equal("test-explicit-fields-user-data"))
		})

		It("Maps GuestOSFamily WINDOWS to 'windows' on CRD spec", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			template := "osac.templates.ocp_virt_vm"

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
						Spec: privatev1.InstanceTypeSpec_builder{
							Vcpus:     4,
							MemoryGib: 8,
						}.Build(),
					}.Build(),
				}.Build(), nil)

			mockDiskImagesClient := NewMockDiskImagesClient(ctrl)
			mockDiskImagesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(privatev1.DiskImagesGetResponse_builder{
					Object: privatev1.DiskImage_builder{
						Id: "windows-image",
						Spec: privatev1.DiskImageSpec_builder{
							SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
							SourceRef:     "quay.io/osac/win2022:latest",
							GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
						}.Build(),
					}.Build(),
				}.Build(), nil)

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
					diskImagesClient:    mockDiskImagesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-windows-guest",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "standard-4-8"},
						DiskImage:    &privatev1.DiskImageReference{Id: "windows-image"},
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.GuestOSFamily).To(Equal("windows"))
			Expect(spec.Image.SourceType).To(Equal(osacv1alpha1.ImageSourceTypeRegistry))
			Expect(spec.Image.SourceRef).To(Equal("quay.io/osac/win2022:latest"))
		})

		It("Returns error when DiskImage Get fails", func() {
			ctx := context.Background()
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			template := "osac.templates.ocp_virt_vm"

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

			mockDiskImagesClient := NewMockDiskImagesClient(ctrl)
			mockDiskImagesClient.EXPECT().
				Get(gomock.Any(), gomock.Any()).
				Return(nil, errors.New("not found"))

			task := &task{
				r: &function{
					logger:              logger,
					instanceTypesClient: mockInstanceTypesClient,
					diskImagesClient:    mockDiskImagesClient,
				},
				computeInstance: privatev1.ComputeInstance_builder{
					Id: "test-disk-image-error",
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     &privatev1.ComputeInstanceTemplateReference{Name: template},
						InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
						DiskImage:    &privatev1.DiskImageReference{Id: "missing-image"},
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			_, err := task.buildSpec(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to resolve disk image 'missing-image'"))
			Expect(err.Error()).To(ContainSubstring("not found"))
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
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
						},
					}.Build(),
				}.Build(),
				hubNamespace: hubNamespace,
				hubClient:    fakeClient,
			}

			spec, err := task.buildSpec(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(spec.VCPUs).To(BeZero())
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
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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

var _ = Describe("registered SSH key resolution", func() {
	const publicKey = testSSHPublicKey

	newTaskWithResponse := func(ctrl *gomock.Controller, response *privatev1.SecretsGetResponse, getErr error) *task {
		instanceTypesClient := NewMockInstanceTypesClient(ctrl)
		instanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{}.Build(),
			}.Build(), nil)
		secretsClient := NewMockSecretsClient(ctrl)
		secretsClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(response, getErr)
		return &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: instanceTypesClient,
				secretsClient:       secretsClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "type-id"}.Build(),
					SshKey:       privatev1.SecretReference_builder{Id: "key-id", Name: "login"}.Build(),
				}.Build(),
			}.Build(),
		}
	}
	newTask := func(ctrl *gomock.Controller, secret *privatev1.Secret, getErr error) *task {
		return newTaskWithResponse(ctrl, privatev1.SecretsGetResponse_builder{Object: secret}.Build(), getErr)
	}

	It("hands the registered public key to the operator spec", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"public_key": []byte(publicKey)},
		}.Build(), nil)

		var spec osacv1alpha1.ComputeInstanceSpec
		Expect(t.addExplicitFields(context.Background(), &spec)).To(Succeed())
		Expect(spec.SSHKey).To(Equal(publicKey))
	})

	DescribeTable("classifies SSH key lookup failures", func(code codes.Code, permanent bool, reason string) {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"public_key": []byte(publicKey)},
		}.Build(), status.Error(code, "lookup failed"))

		err := t.addExplicitFields(context.Background(), &osacv1alpha1.ComputeInstanceSpec{})
		var resolutionErr *controllers.SSHKeyResolutionError
		Expect(errors.As(err, &resolutionErr)).To(BeTrue())
		Expect(resolutionErr.Permanent).To(Equal(permanent))
		Expect(resolutionErr.Reason).To(Equal(reason))
	},
		Entry("not found", codes.NotFound, false, "SecretNotFound"),
		Entry("invalid", codes.InvalidArgument, true, "SecretInvalid"),
		Entry("unavailable", codes.Unavailable, false, ""),
		Entry("deadline", codes.DeadlineExceeded, false, ""),
		Entry("canceled", codes.Canceled, false, ""),
	)

	DescribeTable("classifies invalid SSH key Secrets as permanent", func(secret *privatev1.Secret) {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, secret, nil)

		err := t.addExplicitFields(context.Background(), &osacv1alpha1.ComputeInstanceSpec{})
		var resolutionErr *controllers.SSHKeyResolutionError
		Expect(errors.As(err, &resolutionErr)).To(BeTrue())
		Expect(resolutionErr.Permanent).To(BeTrue())
		Expect(resolutionErr.Reason).To(Equal("SecretInvalid"))
	},
		Entry("wrong Secret type", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_VALUE,
			Data: map[string][]byte{"public_key": []byte(publicKey)},
		}.Build()),
		Entry("missing public_key", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		}.Build()),
		Entry("invalid public key", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"public_key": []byte("not-an-ssh-key")},
		}.Build()),
	)

	It("classifies a missing Secret ID as permanent", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instanceTypesClient := NewMockInstanceTypesClient(ctrl)
		instanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{}.Build(),
			}.Build(), nil)
		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: instanceTypesClient,
				secretsClient:       NewMockSecretsClient(ctrl),
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "type-id"}.Build(),
					SshKey:       privatev1.SecretReference_builder{Name: "login"}.Build(),
				}.Build(),
			}.Build(),
		}

		err := t.addExplicitFields(context.Background(), &osacv1alpha1.ComputeInstanceSpec{})
		var resolutionErr *controllers.SSHKeyResolutionError
		Expect(errors.As(err, &resolutionErr)).To(BeTrue())
		Expect(resolutionErr.Permanent).To(BeTrue())
		Expect(resolutionErr.Reason).To(Equal("SecretInvalid"))
	})
})

var _ = Describe("disk Volume ID handoff", func() {
	newTask := func(
		ctrl *gomock.Controller,
		volumes []*privatev1.ComputeInstanceVolume,
		registrations []*privatev1.ComputeInstanceVolumeRegistration,
		additionalDiskCount int,
	) *task {
		additionalDisks := make([]*privatev1.ComputeInstanceDisk, additionalDiskCount)
		for index := range additionalDisks {
			additionalDisks[index] = privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(int32(40 + index*20)),
			}.Build()
		}
		instanceTypesClient := NewMockInstanceTypesClient(ctrl)
		instanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			AnyTimes().
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{}.Build(),
			}.Build(), nil)

		return &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: instanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-volume-id-handoff",
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: &privatev1.InstanceTypeReference{Id: "type-id"},
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: privatev1.StorageTierReference_builder{Name: "fast"}.Build(),
					}.Build(),
					AdditionalDisks: privatev1.ComputeInstanceDiskList_builder{Items: additionalDisks}.Build(),
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{
					Volumes:            volumes,
					VolumeRegistration: registrations,
				}.Build(),
			}.Build(),
		}
	}

	It("maps persisted IDs by disk key regardless of status order", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, []*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "disk-2", VolumeId: "volume-2"}.Build(),
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
			privatev1.ComputeInstanceVolume_builder{DiskKey: "disk-1", VolumeId: "volume-1"}.Build(),
		}, nil, 2)

		want := map[string]string{
			"boot":   "volume-boot",
			"disk-1": "volume-1",
			"disk-2": "volume-2",
		}
		for range 2 {
			spec, err := t.buildSpecWithNetworkAttachments(context.Background(), false)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.DiskVolumeIDs).To(Equal(want))
		}
	})

	It("copies only durable disk IDs while registration is incomplete", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, []*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
		}, []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-1",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		}, 1)

		spec, err := t.buildSpecWithNetworkAttachments(context.Background(), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.DiskVolumeIDs).To(Equal(map[string]string{
			"boot": "volume-boot",
		}))

		t.computeInstance.GetStatus().SetVolumes(append(
			t.computeInstance.GetStatus().GetVolumes(),
			privatev1.ComputeInstanceVolume_builder{DiskKey: "disk-1", VolumeId: "volume-1"}.Build(),
		))
		spec, err = t.buildSpecWithNetworkAttachments(context.Background(), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.DiskVolumeIDs).To(Equal(map[string]string{
			"boot":   "volume-boot",
			"disk-1": "volume-1",
		}))
	})

	It("rejects a persisted disk entry without a Volume ID", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		t := newTask(ctrl, []*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot"}.Build(),
		}, nil, 0)

		_, err := t.buildSpecWithNetworkAttachments(context.Background(), false)
		Expect(err).To(MatchError(ContainSubstring("empty persisted Volume ID")))
	})
})

var _ = Describe("disk registration handoff from the reconciler", func() {
	const hubID = "hub-for-disk-registration"
	const hubNamespace = "disk-registration-hub"

	newInstance := func() *privatev1.ComputeInstance {
		instance := diskRegistrationTestInstance()
		instance.GetMetadata().SetVersion(1)
		instance.GetStatus().SetHub(hubID)
		instance.GetSpec().SetInstanceType(&privatev1.InstanceTypeReference{Name: "small"})
		return instance
	}
	provisioned := func(instance *privatev1.ComputeInstance) *privatev1.ComputeInstanceCondition {
		for _, condition := range instance.GetStatus().GetConditions() {
			if condition.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
				return condition
			}
		}
		Fail("PROVISIONED condition is missing")
		return nil
	}

	newHarness := func(
		ctrl *gomock.Controller,
		instance *privatev1.ComputeInstance,
		volumesClient *registrationVolumesClient,
		interceptors interceptor.Funcs,
		objects ...clnt.Object,
	) (*function, *registrationComputeInstancesClient, clnt.Client) {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		hubClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithInterceptorFuncs(interceptors).
			Build()
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(gomock.Any(), hubID).Return(&controllers.HubEntry{
			Namespace: hubNamespace,
			Client:    hubClient,
		}, nil).AnyTimes()
		instanceTypesClient := NewMockInstanceTypesClient(ctrl)
		instanceTypesClient.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{Spec: privatev1.InstanceTypeSpec_builder{}.Build()}.Build(),
			}.Build(), nil,
		).AnyTimes()
		computeClient := &registrationComputeInstancesClient{
			object: proto.Clone(instance).(*privatev1.ComputeInstance),
		}
		return &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeClient,
			volumesClient:          volumesClient,
			instanceTypesClient:    instanceTypesClient,
			maskCalculator:         masks.NewCalculator().Build(),
		}, computeClient, hubClient
	}

	newWorkloadRegistrationTask := func() (*task, *registrationComputeInstancesClient, *registrationVolumesClient) {
		instance := newInstance()
		instance.GetSpec().SetAdditionalDisks(nil)
		computeClient := &registrationComputeInstancesClient{object: proto.Clone(instance).(*privatev1.ComputeInstance)}
		volumesClient := &registrationVolumesClient{}
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			return privatev1.VolumesCreateResponse_builder{
				Object: registrationTestVolume(request, "volume-boot", "system"),
			}.Build(), nil
		}
		return &task{
			r:               &function{computeInstancesClient: computeClient, volumesClient: volumesClient},
			computeInstance: instance,
		}, computeClient, volumesClient
	}

	It("reads the parent once after the last resolution write", func() {
		registrationTask, computeClient, _ := newWorkloadRegistrationTask()
		readsAtResolution := -1
		computeClient.afterUpdate = func(call int) {
			if call == 3 {
				readsAtResolution = computeClient.getCalls
			}
		}

		ready, err := registrationTask.reconcileDiskRegistrationForWorkload(context.Background(), true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(readsAtResolution).To(BeNumerically(">", 0))
		Expect(computeClient.getCalls - readsAtResolution).To(Equal(1))
		Expect(registrationTask.registeredVolumes).To(HaveLen(1))
		Expect(registrationTask.registeredVolumes[0].GetVolumeId()).To(Equal("volume-boot"))
	})

	It("reports a failed final read after persisting the Volume ID", func() {
		registrationTask, computeClient, _ := newWorkloadRegistrationTask()
		computeClient.afterUpdate = func(call int) {
			if call == 3 {
				computeClient.getErr = errors.New("final read unavailable")
			}
		}

		ready, err := registrationTask.reconcileDiskRegistrationForWorkload(context.Background(), true)
		Expect(ready).To(BeFalse())
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-boot"))
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(registrationTask.registeredVolumes).To(BeEmpty())
		Expect(provisioned(registrationTask.computeInstance).GetReason()).To(Equal("VolumeRegistrationPending"))
	})

	DescribeTable("stops when the final read observes parent cancellation",
		func(removeFinalizer bool) {
			registrationTask, computeClient, _ := newWorkloadRegistrationTask()
			computeClient.afterUpdate = func(call int) {
				if call != 3 {
					return
				}
				if removeFinalizer {
					computeClient.object.GetMetadata().SetFinalizers(nil)
				} else {
					computeClient.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
				}
			}

			ready, err := registrationTask.reconcileDiskRegistrationForWorkload(context.Background(), true)
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeFalse())
			Expect(computeClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-boot"))
			Expect(registrationTask.registeredVolumes).To(BeEmpty())
		},
		Entry("finalizer removed", true),
		Entry("deletion started", false),
	)

	It("keeps the workload pending when the resolution write loses its lock", func() {
		registrationTask, computeClient, _ := newWorkloadRegistrationTask()
		computeClient.beforeUpdate = func(call int) {
			if call == 3 {
				computeClient.object.GetMetadata().SetVersion(computeClient.object.GetMetadata().GetVersion() + 1)
			}
		}

		ready, err := registrationTask.reconcileDiskRegistrationForWorkload(context.Background(), true)
		Expect(ready).To(BeFalse())
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(computeClient.object.GetStatus().GetVolumes()).To(BeEmpty())
		Expect(registrationStates(computeClient.object)["boot"]).To(Equal(
			privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
		Expect(registrationTask.registeredVolumes).To(BeEmpty())
		Expect(provisioned(registrationTask.computeInstance).GetReason()).To(Equal("VolumeRegistrationPending"))
	})

	It("persists every disk ID before creating the first managed ComputeInstance", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		volumesClient := &registrationVolumesClient{}
		var computeClient *registrationComputeInstancesClient
		created := 0
		f, stored, hubClient := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{
			Create: func(ctx context.Context, client clnt.WithWatch, object clnt.Object, options ...clnt.CreateOption) error {
				if cr, ok := object.(*osacv1alpha1.ComputeInstance); ok {
					created++
					Expect(computeClient.object.GetStatus().GetVolumes()).To(HaveLen(3))
					Expect(registrationStates(computeClient.object)).To(Equal(map[string]privatev1.ComputeInstanceVolumeRegistration_State{
						"boot":   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
						"disk-1": privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
						"disk-2": privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
					}))
					Expect(cr.Spec.DiskVolumeIDs).To(Equal(map[string]string{
						"boot":   "volume-boot",
						"disk-1": "volume-disk-1",
						"disk-2": "volume-disk-2",
					}))
				}
				return client.Create(ctx, object, options...)
			},
		})
		computeClient = stored
		volumesClient.create = func(ctx context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			Expect(created).To(BeZero())
			name := request.GetObject().GetMetadata().GetName()
			Expect(name).To(HavePrefix("ci-" + diskRegistrationTestID + "-"))
			diskKey := name[len("ci-"+diskRegistrationTestID+"-"):]
			Expect(registrationStates(computeClient.object)[diskKey]).To(Equal(
				privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
			Expect(provisioned(computeClient.object).GetReason()).To(Equal("VolumeRegistrationPending"))
			return privatev1.VolumesCreateResponse_builder{
				Object: registrationTestVolume(request, "volume-"+diskKey, "system"),
			}.Build(), nil
		}

		Expect(f.run(context.Background(), instance)).To(Succeed())
		Expect(volumesClient.createRequests).To(HaveLen(3))
		Expect(created).To(Equal(1))
		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		outerUpdates := 0
		for _, request := range computeClient.updateRequests {
			if request.GetLock() {
				continue
			}
			outerUpdates++
			Expect(request.GetUpdateMask().GetPaths()).NotTo(ContainElement("status.volumes"))
			Expect(request.GetUpdateMask().GetPaths()).NotTo(ContainElement("status.volume_registration"))
		}
		Expect(outerUpdates).To(BeNumerically(">", 0))
	})

	It("blocks the managed CR during uncertainty and reuses the recovered ID", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		volumesClient := &registrationVolumesClient{}
		f, computeClient, hubClient := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{})
		var ambiguousRequest *privatev1.VolumesCreateRequest
		createCalls := map[string]int{}
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			name := request.GetObject().GetMetadata().GetName()
			createCalls[name]++
			switch name {
			case "ci-" + diskRegistrationTestID + "-disk-1":
				ambiguousRequest = proto.Clone(request).(*privatev1.VolumesCreateRequest)
				return nil, status.Error(codes.Unavailable, "response lost")
			case "ci-" + diskRegistrationTestID + "-boot":
				return privatev1.VolumesCreateResponse_builder{
					Object: registrationTestVolume(request, "volume-boot", "system"),
				}.Build(), nil
			default:
				return privatev1.VolumesCreateResponse_builder{
					Object: registrationTestVolume(request, "volume-disk-2", "system"),
				}.Build(), nil
			}
		}
		listCalls := 0
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			listCalls++
			if listCalls == 1 {
				return registrationTestListResponse(nil, 0), nil
			}
			return registrationTestListResponse([]*privatev1.Volume{
				registrationTestVolume(ambiguousRequest, "volume-original", "system"),
			}, 1), nil
		}

		Expect(controllers.IsRequeueable(f.run(context.Background(), instance))).To(BeTrue())
		Expect(computeClient.object.GetStatus().GetVolumes()).To(HaveLen(1))
		condition := provisioned(computeClient.object)
		Expect(condition.GetReason()).To(Equal("VolumeRegistrationPending"))
		Expect(condition.GetMessage()).To(ContainSubstring("1/3"))
		Expect(condition.GetMessage()).To(ContainSubstring("disk-1"))
		Expect(condition.GetMessage()).To(ContainSubstring("disk-2"))
		Expect(condition.GetMessage()).NotTo(ContainSubstring("response lost"))
		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())

		restarted := proto.Clone(computeClient.object).(*privatev1.ComputeInstance)
		Expect(f.run(context.Background(), restarted)).To(Succeed())
		Expect(createCalls["ci-"+diskRegistrationTestID+"-boot"]).To(Equal(1))
		Expect(createCalls["ci-"+diskRegistrationTestID+"-disk-1"]).To(Equal(1))
		Expect(createCalls["ci-"+diskRegistrationTestID+"-disk-2"]).To(Equal(1))
		Expect(provisioned(computeClient.object).GetReason()).To(Equal("WaitingForVM"))
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Spec.DiskVolumeIDs).To(Equal(map[string]string{
			"boot":   "volume-boot",
			"disk-1": "volume-original",
			"disk-2": "volume-disk-2",
		}))
	})

	It("reports a confirmed non-commit safely and switches to Pending on retry claim", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		instance.GetSpec().SetAdditionalDisks(nil)
		volumesClient := &registrationVolumesClient{}
		f, computeClient, hubClient := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{})
		createCalls := 0
		volumesClient.create = func(_ context.Context, request *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			createCalls++
			Expect(provisioned(computeClient.object).GetReason()).To(Equal("VolumeRegistrationPending"))
			if createCalls == 1 {
				return nil, status.Error(codes.InvalidArgument, "private provider detail")
			}
			return privatev1.VolumesCreateResponse_builder{
				Object: registrationTestVolume(request, "volume-boot", "system"),
			}.Build(), nil
		}

		Expect(controllers.IsRequeueable(f.run(context.Background(), instance))).To(BeTrue())
		condition := provisioned(computeClient.object)
		Expect(condition.GetReason()).To(Equal("VolumeRegistrationFailed"))
		Expect(condition.GetMessage()).To(ContainSubstring("boot"))
		Expect(condition.GetMessage()).To(ContainSubstring("0/1"))
		Expect(condition.GetMessage()).To(ContainSubstring("retry"))
		Expect(condition.GetMessage()).NotTo(ContainSubstring("private provider detail"))
		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())

		restarted := proto.Clone(computeClient.object).(*privatev1.ComputeInstance)
		Expect(f.run(context.Background(), restarted)).To(Succeed())
		Expect(createCalls).To(Equal(2))
		Expect(provisioned(computeClient.object).GetReason()).To(Equal("WaitingForVM"))
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
	})

	It("requeues an uncertain disk after a complete empty recovery lookup", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		instance.GetSpec().SetAdditionalDisks(nil)
		volumesClient := &registrationVolumesClient{}
		f, computeClient, hubClient := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{})
		volumesClient.create = func(_ context.Context, _ *privatev1.VolumesCreateRequest) (*privatev1.VolumesCreateResponse, error) {
			return nil, status.Error(codes.Unavailable, "lost response")
		}
		volumesClient.list = func(_ context.Context, _ *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return registrationTestListResponse(nil, 0), nil
		}

		Expect(controllers.IsRequeueable(f.run(context.Background(), instance))).To(BeTrue())
		restarted := proto.Clone(computeClient.object).(*privatev1.ComputeInstance)
		Expect(controllers.IsRequeueable(f.run(context.Background(), restarted))).To(BeTrue())
		Expect(volumesClient.createRequests).To(HaveLen(1))
		Expect(volumesClient.listRequests).To(HaveLen(2))
		Expect(provisioned(computeClient.object).GetReason()).To(Equal("VolumeRegistrationPending"))
		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(hubClient.List(context.Background(), list)).To(Succeed())
		Expect(list.Items).To(BeEmpty())
	})

	It("continues an existing workload after all registration IDs are saved", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		instance.GetStatus().SetVolumes([]*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
			privatev1.ComputeInstanceVolume_builder{DiskKey: "disk-1", VolumeId: "volume-disk-1"}.Build(),
			privatev1.ComputeInstanceVolume_builder{DiskKey: "disk-2", VolumeId: "volume-disk-2"}.Build(),
		})
		instance.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "boot", State: privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "disk-1", State: privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "disk-2", State: privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED}.Build(),
		})
		volumesClient := &registrationVolumesClient{}
		patches := 0
		cr := newComputeInstanceCR(diskRegistrationTestID, hubNamespace, "registered", nil)
		f, computeClient, _ := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{
			Patch: func(ctx context.Context, client clnt.WithWatch, object clnt.Object, patch clnt.Patch, options ...clnt.PatchOption) error {
				patches++
				return client.Patch(ctx, object, patch, options...)
			},
		}, cr)
		computeClient.getErr = errors.New("completed workloads should not restart registration")

		Expect(f.run(context.Background(), instance)).To(Succeed())
		Expect(patches).To(Equal(1))
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(volumesClient.listRequests).To(BeEmpty())
	})

	It("holds an existing managed CR while its zero-ID intent remains uncertain", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		instance := newInstance()
		instance.GetSpec().SetAdditionalDisks(nil)
		instance.GetStatus().SetVolumeRegistration([]*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot", State: privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		volumesClient := &registrationVolumesClient{}
		patches := 0
		cr := newComputeInstanceCR(diskRegistrationTestID, hubNamespace, "unresolved", nil)
		f, _, _ := newHarness(ctrl, instance, volumesClient, interceptor.Funcs{
			Patch: func(_ context.Context, _ clnt.WithWatch, _ clnt.Object, _ clnt.Patch, _ ...clnt.PatchOption) error {
				patches++
				return nil
			},
		}, cr)

		Expect(controllers.IsRequeueable(f.run(context.Background(), instance))).To(BeTrue())
		Expect(patches).To(BeZero())
		Expect(volumesClient.createRequests).To(BeEmpty())
		Expect(volumesClient.listRequests).To(HaveLen(1))
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
		logger:            logger,
		hubCache:          hubCache,
		networkingCleanup: &fakeWorkloadNetworkingCleanup{complete: true},
	}

	return &task{
		r:               f,
		computeInstance: ci,
	}
}

type fakeWorkloadNetworkingCleanup struct {
	complete   bool
	calls      int
	workloadID string
	err        error
}

func (f *fakeWorkloadNetworkingCleanup) Cleanup(_ context.Context, workloadID string) (bool, error) {
	f.calls++
	f.workloadID = workloadID
	return f.complete, f.err
}

type volumeCleanupTestClient struct {
	privatev1.VolumesClient
	get            func(context.Context, *privatev1.VolumesGetRequest) (*privatev1.VolumesGetResponse, error)
	delete         func(context.Context, *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error)
	list           func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error)
	getRequests    []*privatev1.VolumesGetRequest
	deleteRequests []*privatev1.VolumesDeleteRequest
	listRequests   []*privatev1.VolumesListRequest
	createCalls    int
}

func (c *volumeCleanupTestClient) Get(
	ctx context.Context,
	request *privatev1.VolumesGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.VolumesGetResponse, error) {
	c.getRequests = append(c.getRequests, proto.Clone(request).(*privatev1.VolumesGetRequest))
	if c.get == nil {
		return nil, status.Error(codes.Unimplemented, "Get not configured")
	}
	return c.get(ctx, request)
}

func (c *volumeCleanupTestClient) Delete(
	ctx context.Context,
	request *privatev1.VolumesDeleteRequest,
	_ ...grpc.CallOption,
) (*privatev1.VolumesDeleteResponse, error) {
	c.deleteRequests = append(c.deleteRequests, proto.Clone(request).(*privatev1.VolumesDeleteRequest))
	if c.delete == nil {
		return nil, status.Error(codes.Unimplemented, "Delete not configured")
	}
	return c.delete(ctx, request)
}

func (c *volumeCleanupTestClient) List(
	ctx context.Context,
	request *privatev1.VolumesListRequest,
	_ ...grpc.CallOption,
) (*privatev1.VolumesListResponse, error) {
	c.listRequests = append(c.listRequests, proto.Clone(request).(*privatev1.VolumesListRequest))
	if c.list == nil {
		return registrationTestListResponse(nil, 0), nil
	}
	return c.list(ctx, request)
}

func (c *volumeCleanupTestClient) Create(
	context.Context,
	*privatev1.VolumesCreateRequest,
	...grpc.CallOption,
) (*privatev1.VolumesCreateResponse, error) {
	c.createCalls++
	return nil, status.Error(codes.Unimplemented, "Create must not run during deletion")
}

func newVolumeCleanupTask(
	volumes []*privatev1.ComputeInstanceVolume,
	registrations []*privatev1.ComputeInstanceVolumeRegistration,
) (*task, *registrationComputeInstancesClient, *volumeCleanupTestClient) {
	instance := diskRegistrationTestInstance()
	instance.GetMetadata().SetFinalizers([]string{finalizers.Controller})
	instance.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
	instance.GetStatus().SetHub("")
	instance.GetStatus().SetVolumes(volumes)
	instance.GetStatus().SetVolumeRegistration(registrations)
	computeInstancesClient := &registrationComputeInstancesClient{
		object: proto.Clone(instance).(*privatev1.ComputeInstance),
	}
	volumesClient := &volumeCleanupTestClient{}
	f := &function{
		logger:                 logger,
		computeInstancesClient: computeInstancesClient,
		volumesClient:          volumesClient,
		networkingCleanup:      &fakeWorkloadNetworkingCleanup{complete: true},
	}
	return &task{r: f, computeInstance: instance}, computeInstancesClient, volumesClient
}

func computeInstanceVolumesForTest(ids ...string) []*privatev1.ComputeInstanceVolume {
	volumes := make([]*privatev1.ComputeInstanceVolume, 0, len(ids))
	for index, id := range ids {
		diskKey := "boot"
		if index > 0 {
			diskKey = fmt.Sprintf("disk-%d", index)
		}
		volumes = append(volumes, privatev1.ComputeInstanceVolume_builder{
			DiskKey:  diskKey,
			VolumeId: id,
		}.Build())
	}
	return volumes
}

func volumeCleanupRecordForTest(instance *privatev1.ComputeInstance, id string, diskKeys ...string) *privatev1.Volume {
	prepared, err := prepareDiskRegistrations(instance)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	diskKey := "boot"
	if len(diskKeys) > 0 {
		diskKey = diskKeys[0]
	}
	var request *privatev1.VolumesCreateRequest
	for _, candidate := range prepared {
		if candidate.diskKey == diskKey {
			request = candidate.request
			break
		}
	}
	ExpectWithOffset(1, request).NotTo(BeNil())
	volume := registrationTestVolume(request, id, "system")
	volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_AVAILABLE)
	return volume
}

func volumeGetResponseForTest(volume *privatev1.Volume) *privatev1.VolumesGetResponse {
	return privatev1.VolumesGetResponse_builder{Object: volume}.Build()
}

type volumeCleanupGetResult struct {
	volume *privatev1.Volume
	err    error
}

func volumeCleanupGetSequence(results ...volumeCleanupGetResult) func(
	context.Context,
	*privatev1.VolumesGetRequest,
) (*privatev1.VolumesGetResponse, error) {
	index := 0
	return func(context.Context, *privatev1.VolumesGetRequest) (*privatev1.VolumesGetResponse, error) {
		if index >= len(results) {
			return nil, status.Error(codes.Internal, "unexpected Volume Get")
		}
		result := results[index]
		index++
		if result.err != nil {
			return nil, result.err
		}
		return volumeGetResponseForTest(result.volume), nil
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

	DescribeTable("requires Get-confirmed absence on every existing finalizer release path",
		func(path string) {
			const volumeID = "volume-recorded"
			t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
			volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{
				err: status.Error(codes.NotFound, "Volume is absent"),
			})

			switch path {
			case "no hub":
			case "decommissioned hub":
				t.computeInstance.GetStatus().SetHub(hubID)
				t.r.hubCache = controllers.NewMockHubCache(ctrl)
				t.r.hubCache.(*controllers.MockHubCache).EXPECT().
					Get(gomock.Any(), hubID).
					Return(nil, controllers.ErrHubNotFound)
			case "missing Kubernetes object":
				t.computeInstance.GetStatus().SetHub(hubID)
				scheme := runtime.NewScheme()
				Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(corev1.AddToScheme(scheme)).To(Succeed())
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				hubCache := controllers.NewMockHubCache(ctrl)
				hubCache.EXPECT().Get(gomock.Any(), hubID).Return(&controllers.HubEntry{
					Namespace: hubNamespace,
					Client:    fakeClient,
				}, nil)
				t.r.hubCache = hubCache
			default:
				Fail("unexpected finalizer release path")
			}

			Expect(t.delete(ctx)).To(Succeed())
			Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
			Expect(volumesClient.getRequests).To(HaveLen(1))
			Expect(volumesClient.getRequests[0].GetId()).To(Equal(volumeID))
			Expect(volumesClient.deleteRequests).To(BeEmpty())
			Expect(volumesClient.createCalls).To(BeZero())
		},
		Entry("no Hub", "no hub"),
		Entry("decommissioned Hub", "decommissioned hub"),
		Entry("missing Kubernetes object", "missing Kubernetes object"),
	)

	It("does not begin Volume cleanup while the Kubernetes ComputeInstance still exists", func() {
		const volumeID = "volume-recorded"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		t.computeInstance.GetStatus().SetHub(hubID)

		cr := newComputeInstanceCR(diskRegistrationTestID, hubNamespace, crName, nil)
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		deleteCalled := false
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(context.Context, clnt.WithWatch, clnt.Object, ...clnt.DeleteOption) error {
					deleteCalled = true
					return nil
				},
			}).
			Build()
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(gomock.Any(), hubID).Return(&controllers.HubEntry{
			Namespace: hubNamespace,
			Client:    fakeClient,
		}, nil)
		t.r.hubCache = hubCache

		Expect(t.delete(ctx)).To(MatchError("kubernetes compute instance deletion is still in progress"))
		Expect(deleteCalled).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(BeEmpty())
		Expect(volumesClient.deleteRequests).To(BeEmpty())
	})

	It("does not begin Volume cleanup while automatic networking resources are terminating", func() {
		const volumeID = "volume-recorded"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		cleanup := &fakeWorkloadNetworkingCleanup{}
		t.r.networkingCleanup = cleanup

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(cleanup.calls).To(Equal(1))
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(BeEmpty())
		Expect(volumesClient.deleteRequests).To(BeEmpty())
	})

	It("deletes a recorded Volume and releases the parent only after Get returns NotFound", func() {
		const volumeID = "volume-recorded"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		volume := volumeCleanupRecordForTest(t.computeInstance, volumeID)
		deleting := proto.Clone(volume).(*privatev1.Volume)
		deleting.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETING)
		volumesClient.get = volumeCleanupGetSequence(
			volumeCleanupGetResult{volume: volume},
			volumeCleanupGetResult{volume: deleting},
		)
		volumesClient.delete = func(_ context.Context, request *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			Expect(request.GetId()).To(Equal(volumeID))
			return privatev1.VolumesDeleteResponse_builder{}.Build(), nil
		}

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(HaveLen(2))
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
		Expect(volumesClient.deleteRequests[0].GetId()).To(Equal(volumeID))
		Expect(t.computeInstance.GetStatus().GetVolumes()).To(Equal(computeInstanceVolumesForTest(volumeID)))
	})

	DescribeTable("waits while a Volume record is already deleting or deleted",
		func(state privatev1.VolumeState) {
			const volumeID = "volume-recorded"
			t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
			volume := volumeCleanupRecordForTest(t.computeInstance, volumeID)
			volume.GetStatus().SetState(state)
			volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{volume: volume})

			err := t.delete(ctx)
			Expect(controllers.IsRequeueable(err)).To(BeTrue())
			Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
			Expect(volumesClient.deleteRequests).To(BeEmpty())
		},
		Entry("DELETING", privatev1.VolumeState_VOLUME_STATE_DELETING),
		Entry("DELETED but still visible", privatev1.VolumeState_VOLUME_STATE_DELETED),
	)

	It("does not treat Delete NotFound as absence when the follow-up Get is inconclusive", func() {
		const volumeID = "volume-recorded"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		volumesClient.get = volumeCleanupGetSequence(
			volumeCleanupGetResult{volume: volumeCleanupRecordForTest(t.computeInstance, volumeID)},
			volumeCleanupGetResult{err: status.Error(codes.Unavailable, "Get unavailable")},
		)
		volumesClient.delete = func(context.Context, *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			return nil, status.Error(codes.NotFound, "already absent")
		}

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(HaveLen(2))
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
	})

	It("requires Get NotFound for every recorded Volume ID", func() {
		firstID, secondID := "volume-boot", "volume-disk-1"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(firstID, secondID), nil)
		volumesClient.get = func(_ context.Context, request *privatev1.VolumesGetRequest) (*privatev1.VolumesGetResponse, error) {
			if request.GetId() == firstID {
				return nil, status.Error(codes.NotFound, "Volume is absent")
			}
			return volumeGetResponseForTest(volumeCleanupRecordForTest(t.computeInstance, secondID, "disk-1")), nil
		}
		volumesClient.delete = func(context.Context, *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			return privatev1.VolumesDeleteResponse_builder{}.Build(), nil
		}

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(HaveLen(3))
		Expect(volumesClient.getRequests[0].GetId()).To(Equal(firstID))
		Expect(volumesClient.getRequests[1].GetId()).To(Equal(secondID))
		Expect(volumesClient.getRequests[2].GetId()).To(Equal(secondID))
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
		Expect(t.computeInstance.GetStatus().GetVolumes()).To(HaveLen(2))
		Expect(t.computeInstance.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(firstID))
		Expect(t.computeInstance.GetStatus().GetVolumes()[1].GetVolumeId()).To(Equal(secondID))
	})

	It("releases only after Get reports every recorded Volume ID absent", func() {
		firstID, secondID := "volume-boot", "volume-disk-1"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(firstID, secondID), nil)
		volumesClient.get = func(_ context.Context, request *privatev1.VolumesGetRequest) (*privatev1.VolumesGetResponse, error) {
			Expect(request.GetId()).To(BeElementOf(firstID, secondID))
			return nil, status.Error(codes.NotFound, "Volume is absent")
		}

		Expect(t.delete(ctx)).To(Succeed())
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
		Expect(volumesClient.getRequests).To(HaveLen(2))
		Expect(volumesClient.getRequests[0].GetId()).To(Equal(firstID))
		Expect(volumesClient.getRequests[1].GetId()).To(Equal(secondID))
		Expect(volumesClient.deleteRequests).To(BeEmpty())
	})

	It("retries a lost Delete response by checking the original ID before retrying", func() {
		const volumeID = "volume-recorded"
		t, computeInstancesClient, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{
			volume: volumeCleanupRecordForTest(t.computeInstance, volumeID),
		})
		volumesClient.delete = func(context.Context, *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			return nil, status.Error(codes.Unavailable, "Delete response was lost")
		}

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(t.computeInstance.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(volumeID))

		volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{
			err: status.Error(codes.NotFound, "Volume is absent"),
		})
		restarted := &task{
			r:               t.r,
			computeInstance: proto.Clone(computeInstancesClient.object).(*privatev1.ComputeInstance),
		}
		Expect(restarted.delete(ctx)).To(Succeed())
		Expect(hasFinalizer(restarted.computeInstance)).To(BeFalse())
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
		Expect(volumesClient.getRequests[1].GetId()).To(Equal(volumeID))
	})

	DescribeTable("fails closed when Get returns a record outside the parent scope or attribution",
		func(mutate func(*privatev1.Volume)) {
			const volumeID = "volume-recorded"
			t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
			volume := volumeCleanupRecordForTest(t.computeInstance, volumeID)
			mutate(volume)
			volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{volume: volume})

			err := t.delete(ctx)
			Expect(controllers.IsRequeueable(err)).To(BeTrue())
			Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
			Expect(volumesClient.deleteRequests).To(BeEmpty())
		},
		Entry("foreign tenant", func(volume *privatev1.Volume) {
			volume.GetMetadata().SetTenant("tenant-b")
		}),
		Entry("foreign project", func(volume *privatev1.Volume) {
			volume.GetMetadata().SetProject("project-b")
		}),
		Entry("unexpected creator", func(volume *privatev1.Volume) {
			volume.GetMetadata().SetCreator("tenant-user")
		}),
		Entry("name belongs to a different parent or disk", func(volume *privatev1.Volume) {
			volume.GetMetadata().SetName("ci-another-parent-boot")
		}),
	)

	DescribeTable("keeps the parent finalizer when Get is not authoritative",
		func(code codes.Code) {
			const volumeID = "volume-recorded"
			t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
			volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{
				err: status.Error(code, "Volume lookup failed"),
			})

			err := t.delete(ctx)
			Expect(controllers.IsRequeueable(err)).To(BeTrue())
			Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
			Expect(volumesClient.deleteRequests).To(BeEmpty())
		},
		Entry("authorization denied", codes.PermissionDenied),
		Entry("service unavailable", codes.Unavailable),
		Entry("deadline exceeded", codes.DeadlineExceeded),
	)

	It("keeps the parent finalizer when Delete is denied", func() {
		const volumeID = "volume-recorded"
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(volumeID), nil)
		volumesClient.get = volumeCleanupGetSequence(volumeCleanupGetResult{
			volume: volumeCleanupRecordForTest(t.computeInstance, volumeID),
		})
		volumesClient.delete = func(context.Context, *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "Delete denied")
		}

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(HaveLen(1))
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
	})

	DescribeTable("does not create or block on known non-committed cancellation states",
		func(state privatev1.ComputeInstanceVolumeRegistration_State) {
			t, _, volumesClient := newVolumeCleanupTask(nil, []*privatev1.ComputeInstanceVolumeRegistration{
				privatev1.ComputeInstanceVolumeRegistration_builder{DiskKey: "boot", State: state}.Build(),
			})

			Expect(t.delete(ctx)).To(Succeed())
			Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
			Expect(volumesClient.listRequests).To(BeEmpty())
			Expect(volumesClient.getRequests).To(BeEmpty())
			Expect(volumesClient.deleteRequests).To(BeEmpty())
			Expect(volumesClient.createCalls).To(BeZero())
		},
		Entry("never attempted", privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED),
		Entry("known non-commit", privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE),
	)

	It("preserves legacy deletion when registration state and recorded IDs are absent", func() {
		t, _, volumesClient := newVolumeCleanupTask(nil, nil)

		Expect(t.delete(ctx)).To(Succeed())
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
		Expect(volumesClient.listRequests).To(BeEmpty())
		Expect(volumesClient.getRequests).To(BeEmpty())
		Expect(volumesClient.deleteRequests).To(BeEmpty())
		Expect(volumesClient.createCalls).To(BeZero())
	})

	It("fails closed on an empty persisted Volume ID", func() {
		t, _, volumesClient := newVolumeCleanupTask(computeInstanceVolumesForTest(""), nil)

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.getRequests).To(BeEmpty())
		Expect(volumesClient.deleteRequests).To(BeEmpty())
	})

	It("recovers and deletes the original uncertain registration without creating a replacement", func() {
		const recoveredID = "volume-original"
		t, computeInstancesClient, volumesClient := newVolumeCleanupTask(nil, []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		prepared, err := prepareDiskRegistrations(t.computeInstance)
		Expect(err).NotTo(HaveOccurred())
		recovered := registrationTestVolume(prepared[0].request, recoveredID, "system")
		recovered.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_AVAILABLE)
		volumesClient.list = func(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
			return registrationTestListResponse([]*privatev1.Volume{recovered}, 1), nil
		}
		volumesClient.get = volumeCleanupGetSequence(
			volumeCleanupGetResult{volume: recovered},
			volumeCleanupGetResult{err: status.Error(codes.NotFound, "Volume is absent")},
		)
		volumesClient.delete = func(_ context.Context, request *privatev1.VolumesDeleteRequest) (*privatev1.VolumesDeleteResponse, error) {
			Expect(request.GetId()).To(Equal(recoveredID))
			return privatev1.VolumesDeleteResponse_builder{}.Build(), nil
		}

		Expect(t.delete(ctx)).To(Succeed())
		Expect(hasFinalizer(t.computeInstance)).To(BeFalse())
		Expect(computeInstancesClient.object.GetStatus().GetVolumes()).To(HaveLen(1))
		Expect(computeInstancesClient.object.GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
		Expect(computeInstancesClient.object.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal(recoveredID))
		Expect(computeInstancesClient.object.GetStatus().GetVolumeRegistration()[0].GetState()).To(
			Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		Expect(volumesClient.createCalls).To(BeZero())
		Expect(volumesClient.listRequests).To(HaveLen(1))
		Expect(volumesClient.deleteRequests).To(HaveLen(1))
	})

	It("blocks cleanup when the post-recovery read contains an unknown disk key", func() {
		deleting, computeClient, volumesClient := newVolumeCleanupTask(nil, []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})
		computeClient.beforeGet = func(call int) {
			if call == 3 {
				computeClient.object.GetStatus().GetVolumeRegistration()[0].SetDiskKey("other")
				computeClient.object.GetStatus().GetVolumeRegistration()[0].SetState(
					privatev1.ComputeInstanceVolumeRegistration_STATE_NEVER_ATTEMPTED)
			}
		}

		err := deleting.releaseFinalizerAfterVolumeCleanup(context.Background(), deleting.removeFinalizer)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("unknown disk")))
		Expect(computeClient.getCalls).To(Equal(3))
		Expect(hasFinalizer(deleting.computeInstance)).To(BeTrue())
		Expect(volumesClient.createCalls).To(BeZero())
		Expect(volumesClient.deleteRequests).To(BeEmpty())
	})

	It("retains an uncertain registration when recovery finds no record", func() {
		t, _, volumesClient := newVolumeCleanupTask(nil, []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN,
			}.Build(),
		})

		err := t.delete(ctx)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
		Expect(volumesClient.listRequests).To(HaveLen(1))
		Expect(volumesClient.createCalls).To(BeZero())
		Expect(volumesClient.getRequests).To(BeEmpty())
		Expect(t.computeInstance.GetStatus().GetVolumeRegistration()[0].GetState()).To(
			Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_UNKNOWN))
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

	It("should retain the finalizer while automatic networking resources are terminating", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(gomock.Any(), hubID).Return(&controllers.HubEntry{
			Namespace: hubNamespace, Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		}, nil)
		t := newTaskForDelete(ciID, hubID, hubCache)
		cleanup := &fakeWorkloadNetworkingCleanup{}
		t.r.networkingCleanup = cleanup

		Expect(t.delete(ctx)).To(MatchError(ContainSubstring("automatic workload networking resources are still terminating")))
		Expect(cleanup.calls).To(Equal(1))
		Expect(cleanup.workloadID).To(Equal(ciID))
		Expect(hasFinalizer(t.computeInstance)).To(BeTrue())
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
		Expect(err).To(MatchError("kubernetes compute instance deletion is still in progress"))
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
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
		Expect(err).To(MatchError("kubernetes compute instance deletion is still in progress"))
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
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
			logger:            logger,
			networkingCleanup: &fakeWorkloadNetworkingCleanup{complete: true},
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

var _ = Describe("automatic ExternalIP attachment", func() {
	It("waits for RUNNING, Ready, and one matching attachment IP", func() {
		ci := newReadyAutoExternalIPComputeInstance()
		Expect(computeInstanceReadyForExternalIPAttachment(ci)).To(BeTrue())

		cases := []struct {
			name   string
			mutate func(*privatev1.ComputeInstance)
		}{
			{
				name: "not running",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING)
				},
			},
			{
				name: "not ready",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().GetConditions()[0].SetStatus(privatev1.ConditionStatus_CONDITION_STATUS_FALSE)
				},
			},
			{
				name: "missing network attachment",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetSpec().SetNetworkAttachments(nil)
				},
			},
			{
				name: "multiple network attachments",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetSpec().SetNetworkAttachments(append(ci.GetSpec().GetNetworkAttachments(),
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: "subnet-2"}}.Build()))
				},
			},
			{
				name: "missing attachment status",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().SetComputeNetworkAttachmentStatuses(nil)
				},
			},
			{
				name: "multiple attachment statuses",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().SetComputeNetworkAttachmentStatuses(append(ci.GetStatus().GetComputeNetworkAttachmentStatuses(),
						privatev1.ComputeNetworkAttachmentStatus_builder{SubnetRef: "subnet-2", IpAddress: "10.0.1.4"}.Build()))
				},
			},
			{
				name: "missing IP address",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().GetComputeNetworkAttachmentStatuses()[0].SetIpAddress("")
				},
			},
			{
				name: "malformed IP address",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().GetComputeNetworkAttachmentStatuses()[0].SetIpAddress("not-an-ip")
				},
			},
			{
				name: "status belongs to another subnet",
				mutate: func(ci *privatev1.ComputeInstance) {
					ci.GetStatus().GetComputeNetworkAttachmentStatuses()[0].SetSubnetRef("stale-subnet")
				},
			},
		}

		for _, tc := range cases {
			By(tc.name)
			ci := proto.Clone(newReadyAutoExternalIPComputeInstance()).(*privatev1.ComputeInstance)
			tc.mutate(ci)
			Expect(computeInstanceReadyForExternalIPAttachment(ci)).To(BeFalse())
		}
	})

	It("creates one labeled attachment only after the auto-created ExternalIP is allocated", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		ci := newReadyAutoExternalIPComputeInstance()
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
				Expect(request.GetFilter()).To(ContainSubstring("ci-auto-eip"))
				Expect(request.GetFilter()).To(ContainSubstring("tenant-a"))
				return privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil
			})
		externalIPsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ExternalIPsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIP{newAutoCreatedExternalIP("eip-1", "ci-auto-eip")},
			}.Build(), nil)
		attachmentsClient.EXPECT().
			Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.ExternalIPAttachmentsCreateRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error) {
				attachment := request.GetObject()
				Expect(attachment.GetMetadata().GetTenant()).To(Equal("tenant-a"))
				Expect(attachment.GetMetadata().GetName()).To(Equal("auto-eipa-ci-auto-eip"))
				Expect(attachment.GetMetadata().GetLabels()).To(HaveKeyWithValue(labels.AutoCreated, "true"))
				Expect(attachment.GetMetadata().GetLabels()).To(HaveKeyWithValue(labels.AutoCreatedFor, "ci-auto-eip"))
				Expect(attachment.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(annotations.OwnerReference, "ci-auto-eip"))
				Expect(attachment.GetStatus()).To(BeNil())
				Expect(attachment.GetSpec().GetExternalIp().GetId()).To(Equal("eip-1"))
				Expect(attachment.GetSpec().GetComputeInstance().GetId()).To(Equal("ci-auto-eip"))
				return privatev1.ExternalIPAttachmentsCreateResponse_builder{Object: attachment}.Build(), nil
			})

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: ci,
		}
		Expect(t.reconcileAutoExternalIPAttachment(context.Background())).To(Succeed())
	})

	It("waits while the ExternalIP is pending", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil)
		externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIP{{
					Metadata: privatev1.Metadata_builder{
						Annotations: map[string]string{annotations.OwnerReference: "ci-auto-eip"},
					}.Build(),
					Status: privatev1.ExternalIPStatus_builder{
						State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
					}.Build(),
				}},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		Expect(t.reconcileAutoExternalIPAttachment(context.Background())).To(Succeed())
	})

	It("does not create a second attachment when one already exists", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIPAttachment{newAutoExternalIPAttachment("eipa-1", "ci-auto-eip")},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		Expect(t.reconcileAutoExternalIPAttachment(context.Background())).To(Succeed())
	})

	It("treats AlreadyExists as a concurrent successful create after confirming the attachment", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil)
		externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIP{newAutoCreatedExternalIP("eip-1", "ci-auto-eip")},
			}.Build(), nil)
		attachmentsClient.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.AlreadyExists, "created concurrently"))
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIPAttachment{newAutoExternalIPAttachment("eipa-1", "ci-auto-eip")},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		Expect(t.reconcileAutoExternalIPAttachment(context.Background())).To(Succeed())
	})

	It("deletes the attachment first and waits before listing or deleting its ExternalIP", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachment := newAutoExternalIPAttachment("eipa-1", "ci-auto-eip")
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{Total: 1, Items: []*privatev1.ExternalIPAttachment{attachment}}.Build(), nil)
		attachmentsClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ExternalIPAttachmentsDeleteRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsDeleteResponse, error) {
				Expect(request.GetId()).To(Equal("eipa-1"))
				return privatev1.ExternalIPAttachmentsDeleteResponse_builder{}.Build(), nil
			})

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		complete, err := t.cleanupAutoExternalIPResources(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(complete).To(BeFalse())
	})

	It("waits while an ExternalIP is attached or already deleting", func() {
		for _, deleting := range []bool{false, true} {
			ctrl := gomock.NewController(GinkgoT())
			DeferCleanup(ctrl.Finish)
			attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
			externalIPsClient := NewMockExternalIPsClient(ctrl)
			attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
				privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil)
			metadata := privatev1.Metadata_builder{
				Tenant: "tenant-a",
				Labels: map[string]string{
					labels.AutoCreated:    "true",
					labels.AutoCreatedFor: "ci-auto-eip",
				},
				Annotations: map[string]string{annotations.OwnerReference: "ci-auto-eip"},
			}.Build()
			if deleting {
				metadata.SetDeletionTimestamp(timestamppb.Now())
			}
			externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
				privatev1.ExternalIPsListResponse_builder{
					Total: 1,
					Items: []*privatev1.ExternalIP{{
						Metadata: metadata,
						Status: privatev1.ExternalIPStatus_builder{
							Attached: !deleting,
						}.Build(),
					}},
				}.Build(), nil)
			t := &task{
				r: &function{
					logger:                      logger,
					externalIPAttachmentsClient: attachmentsClient,
					externalIPsClient:           externalIPsClient,
				},
				computeInstance: newReadyAutoExternalIPComputeInstance(),
			}
			complete, err := t.cleanupAutoExternalIPResources(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(complete).To(BeFalse())
		}
	})

	It("blocks cleanup on a manually managed attachment", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIPAttachment{{
					Id: "manual-eipa",
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}},
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		complete, err := t.cleanupAutoExternalIPResources(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(complete).To(BeFalse())
	})

	It("starts ExternalIP deletion only after the attachment is absent", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil)
		externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIP{newAutoCreatedExternalIP("eip-1", "ci-auto-eip")},
			}.Build(), nil)
		externalIPsClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.ExternalIPsDeleteRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPsDeleteResponse, error) {
				Expect(request.GetId()).To(Equal("eip-1"))
				return privatev1.ExternalIPsDeleteResponse_builder{}.Build(), nil
			})

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		complete, err := t.cleanupAutoExternalIPResources(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(complete).To(BeFalse())
	})

	It("treats a concurrent attachment NotFound as already removed", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIPAttachment{newAutoExternalIPAttachment("eipa-1", "ci-auto-eip")},
			}.Build(), nil)
		attachmentsClient.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(
			nil, status.Error(codes.NotFound, "already deleted"))
		externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPsListResponse_builder{}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		complete, err := t.cleanupAutoExternalIPResources(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(complete).To(BeTrue())
	})

	It("keeps the ComputeInstance finalizer while attachment cleanup is asynchronous", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{
				Total: 1,
				Items: []*privatev1.ExternalIPAttachment{newAutoExternalIPAttachment("eipa-1", "ci-auto-eip")},
			}.Build(), nil)
		attachmentsClient.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsDeleteResponse_builder{}.Build(), nil)

		ci := newReadyAutoExternalIPComputeInstance()
		ci.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		ci.GetMetadata().SetFinalizers([]string{finalizers.Controller})
		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           NewMockExternalIPsClient(ctrl),
			},
			computeInstance: ci,
		}

		Expect(t.delete(context.Background())).To(Succeed())
		Expect(hasFinalizer(ci)).To(BeTrue())
	})

	It("completes cleanup after both dependent resources are absent", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		attachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPsClient := NewMockExternalIPsClient(ctrl)
		attachmentsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPAttachmentsListResponse_builder{}.Build(), nil)
		externalIPsClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.ExternalIPsListResponse_builder{}.Build(), nil)

		t := &task{
			r: &function{
				logger:                      logger,
				externalIPAttachmentsClient: attachmentsClient,
				externalIPsClient:           externalIPsClient,
			},
			computeInstance: newReadyAutoExternalIPComputeInstance(),
		}
		complete, err := t.cleanupAutoExternalIPResources(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(complete).To(BeTrue())
	})
})

func newReadyAutoExternalIPComputeInstance() *privatev1.ComputeInstance {
	return privatev1.ComputeInstance_builder{
		Id: "ci-auto-eip",
		Metadata: privatev1.Metadata_builder{
			Name:   "vm-auto-eip",
			Tenant: "tenant-a",
		}.Build(),
		Spec: privatev1.ComputeInstanceSpec_builder{
			AutoExternalIpAttachment: proto.Bool(true),
			NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
				privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: "subnet-1"}}.Build(),
			},
		}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{
			State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			Conditions: []*privatev1.ComputeInstanceCondition{
				privatev1.ComputeInstanceCondition_builder{
					Type:   privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build(),
			},
			ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
				privatev1.ComputeNetworkAttachmentStatus_builder{SubnetRef: "subnet-1", IpAddress: "10.0.0.5"}.Build(),
			},
		}.Build(),
	}.Build()
}

func newAutoCreatedExternalIP(id, ciID string) *privatev1.ExternalIP {
	return privatev1.ExternalIP_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Tenant: "tenant-a",
			Labels: map[string]string{
				labels.AutoCreated:    "true",
				labels.AutoCreatedFor: ciID,
			},
			Annotations: map[string]string{annotations.OwnerReference: ciID},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{
			State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			Address: "192.0.2.20",
		}.Build(),
	}.Build()
}

func newAutoExternalIPAttachment(id, ciID string) *privatev1.ExternalIPAttachment {
	return privatev1.ExternalIPAttachment_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Tenant: "tenant-a",
			Labels: map[string]string{
				labels.AutoCreated:    "true",
				labels.AutoCreatedFor: ciID,
			},
			Annotations: map[string]string{annotations.OwnerReference: ciID},
		}.Build(),
		Spec: privatev1.ExternalIPAttachmentSpec_builder{
			ExternalIp:      &privatev1.ExternalIPLocalReference{Id: "eip-1"},
			ComputeInstance: &privatev1.ComputeInstanceLocalReference{Id: ciID},
		}.Build(),
	}.Build()
}

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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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

	It("should populate the sole networkAttachment", func() {
		sid1 := "subnet-id-1"
		subnetCR1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "sn-1",
				Labels:    map[string]string{labels.SubnetUuid: sid1},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(subnetCR1).
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: sid1}}.Build(),
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
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
					UserDataSource: privatev1.UserDataSource_builder{Inline: new("#cloud-config\npackages:\n  - vim")}.Build(),
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

	It("should create a Secret from a referenced OSAC Secret", func() {
		ctrl := gomock.NewController(GinkgoT())
		secretsClient := NewMockSecretsClient(ctrl)
		secretsClient.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, request *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
				Expect(request.GetId()).To(Equal("source-secret-id"))
				return privatev1.SecretsGetResponse_builder{Object: privatev1.Secret_builder{
					Data: map[string][]byte{userDataSecretKey: []byte("referenced-data")},
				}.Build()}.Build(), nil
			},
		)
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		t := &task{
			r: &function{logger: logger, secretsClient: secretsClient},
			computeInstance: privatev1.ComputeInstance_builder{Id: ciID, Spec: privatev1.ComputeInstanceSpec_builder{
				UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "source-secret-id"}.Build()}.Build(),
			}.Build()}.Build(),
			hubNamespace: hubNamespace, hubClient: fakeClient, userDataSecretName: ciID + userDataSecretSuffix,
		}
		Expect(t.ensureUserDataSecret(ctx, owner)).To(Succeed())
		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, clnt.ObjectKey{Namespace: hubNamespace, Name: ciID + userDataSecretSuffix}, secret)).To(Succeed())
		Expect(secret.StringData[userDataSecretKey]).To(Equal("referenced-data"))
	})

	It("should reject a referenced OSAC Secret without userdata", func() {
		ctrl := gomock.NewController(GinkgoT())
		secretsClient := NewMockSecretsClient(ctrl)
		secretsClient.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			privatev1.SecretsGetResponse_builder{Object: privatev1.Secret_builder{Data: map[string][]byte{"other": []byte("value")}}.Build()}.Build(), nil)
		t := &task{r: &function{logger: logger, secretsClient: secretsClient},
			computeInstance: privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{
				UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "source-secret-id"}.Build()}.Build(),
			}.Build()}.Build(), userDataSecretName: ciID + userDataSecretSuffix}
		Expect(t.ensureUserDataSecret(ctx, owner)).To(MatchError(ContainSubstring("missing non-empty data")))
	})

	It("should preserve user data when Secret already exists", func() {
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: hubNamespace, Name: ciID + userDataSecretSuffix},
			StringData: map[string]string{userDataSecretKey: "old-data"},
		}

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
					UserDataSource: privatev1.UserDataSource_builder{Inline: new("some-data")}.Build(),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: ciID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())
		secret := &corev1.Secret{}
		Expect(fakeClient.Get(ctx, clnt.ObjectKey{Namespace: hubNamespace, Name: ciID + userDataSecretSuffix}, secret)).To(Succeed())
		Expect(secret.StringData[userDataSecretKey]).To(Equal("old-data"))
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
					UserDataSource: privatev1.UserDataSource_builder{Inline: new("some-data")}.Build(),
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

	It("skips hub selection and preserves disk status when already set", func() {

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
		volumes := []*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-reconcile"}.Build(),
		}
		registrations := []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			}.Build(),
		}

		// Only expect final update (no hub persistence update)
		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				// Verify status.hub is NOT in the field mask (already set, no update needed)
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.volumes"))
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.volume_registration"))
				Expect(req.GetObject().GetStatus().GetVolumes()).To(Equal(volumes))
				Expect(req.GetObject().GetStatus().GetVolumeRegistration()).To(Equal(registrations))
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
				BootDisk: privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(20),
					StorageTier: privatev1.StorageTierReference_builder{Name: "fast"}.Build(),
				}.Build(),
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State:              privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				Hub:                hubID, // Hub already set
				Volumes:            volumes,
				VolumeRegistration: registrations,
			}.Build(),
		}.Build()
		computeInstancesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			AnyTimes().
			Return(privatev1.ComputeInstancesGetResponse_builder{Object: computeInstance}.Build(), nil)

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			computeInstancesClient: computeInstancesClient,
			volumesClient:          &registrationVolumesClient{},
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

	It("preserves disk status while persisting the deletion finalizer", func() {
		volumes := []*privatev1.ComputeInstanceVolume{
			privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-deleting"}.Build(),
		}
		registrations := []*privatev1.ComputeInstanceVolumeRegistration{
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "boot",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
			}.Build(),
			privatev1.ComputeInstanceVolumeRegistration_builder{
				DiskKey: "disk-1",
				State:   privatev1.ComputeInstanceVolumeRegistration_STATE_NON_COMMITTED_FAILURE,
			}.Build(),
		}
		computeInstancesClient := NewMockComputeInstancesClient(ctrl)
		computeInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.volumes"))
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.volume_registration"))
				Expect(req.GetObject().GetStatus().GetVolumes()).To(Equal(volumes))
				Expect(req.GetObject().GetStatus().GetVolumeRegistration()).To(Equal(registrations))
				return &privatev1.ComputeInstancesUpdateResponse{Object: req.GetObject()}, nil
			})

		metadata := privatev1.Metadata_builder{
			Tenant:     tenantName,
			Finalizers: []string{finalizers.Controller},
		}.Build()
		metadata.SetDeletionTimestamp(timestamppb.Now())
		computeInstance := privatev1.ComputeInstance_builder{
			Id:       computeInstanceID,
			Metadata: metadata,
			Spec:     privatev1.ComputeInstanceSpec_builder{}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				Volumes:            volumes,
				VolumeRegistration: registrations,
			}.Build(),
		}.Build()
		f := &function{
			logger:                 logger,
			computeInstancesClient: computeInstancesClient,
			volumesClient: &volumeCleanupTestClient{
				get: volumeCleanupGetSequence(volumeCleanupGetResult{
					err: status.Error(codes.NotFound, "Volume is absent"),
				}),
			},
			networkingCleanup: &fakeWorkloadNetworkingCleanup{complete: true},
		}

		Expect(f.run(ctx, computeInstance)).To(Succeed())
		Expect(computeInstance.GetMetadata().GetFinalizers()).To(BeEmpty())
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

	It("resolves instance_type to vCPUs/memory_gib on CR spec", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "test-type",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.VCPUs).To(Equal(int32(4)))
		Expect(spec.MemoryGiB).To(Equal(int32(8)))
	})

	It("resolves instance_type gpu fields onto CR spec", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "gpu-a100-8core",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     8,
						MemoryGib: 64,
						Gpu: privatev1.GpuSpec_builder{
							PciDeviceSelector: "10DE:20B0",
							ResourceName:      "nvidia.com/A100",
							Count:             1,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build(), nil)

		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-gpu-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: "gpu-a100-8core"},
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.VCPUs).To(Equal(int32(8)))
		Expect(spec.MemoryGiB).To(Equal(int32(64)))
		Expect(spec.Gpu).ToNot(BeNil())
		Expect(spec.Gpu.PciDeviceSelector).To(Equal("10DE:20B0"))
		Expect(spec.Gpu.ResourceName).To(Equal("nvidia.com/A100"))
		Expect(spec.Gpu.Count).To(Equal(int32(1)))
	})

	It("leaves gpu nil when InstanceType has no gpu", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "standard-4-8",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
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
				Id: "test-no-gpu-instance",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: "standard-4-8"},
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.VCPUs).To(Equal(int32(4)))
		Expect(spec.MemoryGiB).To(Equal(int32(8)))
		Expect(spec.Gpu).To(BeNil())
	})

	It("gpu stamping is idempotent", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "gpu-a100-8core",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     8,
						MemoryGib: 64,
						Gpu: privatev1.GpuSpec_builder{
							PciDeviceSelector: "10DE:20B0",
							ResourceName:      "nvidia.com/A100",
							Count:             2,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build(), nil).
			Times(2)

		t := &task{
			r: &function{
				logger:              logger,
				instanceTypesClient: mockInstanceTypesClient,
			},
			computeInstance: privatev1.ComputeInstance_builder{
				Id: "test-idempotent-gpu",
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: "gpu-a100-8core"},
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		spec1, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())

		spec2, err := t.buildSpec(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(spec1.Gpu).ToNot(BeNil())
		Expect(spec2.Gpu).ToNot(BeNil())
		Expect(*spec1.Gpu).To(Equal(*spec2.Gpu))
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
						Vcpus:     4,
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
				NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
					privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
			networkingHubReader:    readyComputeNetworkingHubReader("test-hub", hubNamespace, fakeClient),
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

	It("updates osac.io/instance-type-name label on CR when instance_type changes", func() {
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "test-type",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
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

		// Seed an existing CR reconciled toward the previous type:
		existingCR := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "existing-vm",
				Labels: map[string]string{
					labels.ComputeInstanceUuid: "test-instance-label-update",
					labels.InstanceTypeName:    "old-type",
				},
			},
			Spec: osacv1alpha1.ComputeInstanceSpec{
				TemplateID: "osac.templates.ocp_virt_vm",
				VCPUs:      2,
				MemoryGiB:  4,
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{
					{SubnetRef: "test-sn"},
				},
			},
		}
		Expect(fakeClient.Create(ctx, existingCR)).To(Succeed())

		computeInstance := privatev1.ComputeInstance_builder{
			Id: "test-instance-label-update",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
				InstanceType: &privatev1.InstanceTypeReference{Name: "test-type"},
				NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
					privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
			networkingHubReader:    readyComputeNetworkingHubReader("test-hub", hubNamespace, fakeClient),
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}

		err := f.run(ctx, computeInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify the CR spec was patched and the label follows the new type
		list := &osacv1alpha1.ComputeInstanceList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Spec.VCPUs).To(Equal(int32(4)))
		Expect(list.Items[0].Labels).To(HaveKeyWithValue(labels.InstanceTypeName, "test-type"))
	})

	It("moves only the instance-type-name label on a name-only resize", func() {
		// Two instance type names can resolve to identical vCPUs, memory, and
		// GPU specs. The resize then changes nothing the CR spec hashes: the
		// desired config version stays put and the operator's provisioning job
		// has nothing to do, so only the label moves. The label is what the
		// operator gates the running-type stamp on, so it must move even though
		// the spec does not.
		mockInstanceTypesClient := NewMockInstanceTypesClient(ctrl)
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Id: "test-type",
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
						MemoryGib: 8,
					}.Build(),
				}.Build(),
			}.Build(), nil).
			AnyTimes()

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

		instanceWith := func(typeName string) *privatev1.ComputeInstance {
			return privatev1.ComputeInstance_builder{
				Id: "test-instance-name-only-resize",
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
					Tenant:     "test-tenant",
				}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template:     &privatev1.ComputeInstanceTemplateReference{Name: "osac.templates.ocp_virt_vm"},
					InstanceType: &privatev1.InstanceTypeReference{Name: typeName},
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
					},
				}.Build(),
				Status: privatev1.ComputeInstanceStatus_builder{
					State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					Hub:   "test-hub",
				}.Build(),
			}.Build()
		}

		f := &function{
			logger:                 logger,
			hubCache:               hubCache,
			networkingHubReader:    readyComputeNetworkingHubReader("test-hub", hubNamespace, fakeClient),
			computeInstancesClient: computeInstancesClient,
			hubsClient:             hubsClient,
			instanceTypesClient:    mockInstanceTypesClient,
			maskCalculator:         nil,
		}

		// The first reconcile creates the CR toward the previous type.
		Expect(f.run(ctx, instanceWith("old-type"))).ToNot(HaveOccurred())

		list := &osacv1alpha1.ComputeInstanceList{}
		Expect(fakeClient.List(ctx, list)).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Labels).To(HaveKeyWithValue(labels.InstanceTypeName, "old-type"))
		specBefore := list.Items[0].Spec

		// The second reconcile applies a type whose name differs but whose
		// resolved resources are identical.
		Expect(f.run(ctx, instanceWith("new-type"))).ToNot(HaveOccurred())

		list = &osacv1alpha1.ComputeInstanceList{}
		Expect(fakeClient.List(ctx, list)).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		// The spec the config version hashes is unchanged...
		Expect(list.Items[0].Spec).To(Equal(specBefore))
		// ...while the label names the new type.
		Expect(list.Items[0].Labels).To(HaveKeyWithValue(labels.InstanceTypeName, "new-type"))
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
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
				NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
					privatev1.ComputeNetworkAttachment_builder{Subnet: &privatev1.SubnetLocalReference{Id: subnetID}}.Build(),
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
			networkingHubReader:    readyComputeNetworkingHubReader(hubID, hubNamespace, fakeClient),
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
						Vcpus:     150,
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
								field.NewPath("spec", "vcpus"),
								150,
								"spec.vcpus in body should be less than or equal to 128",
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
		Expect(configCondition.GetMessage()).To(ContainSubstring("spec.vcpus"))
		Expect(configCondition.GetMessage()).To(ContainSubstring("128"))
	})

	It("should set state to FAILED when K8s Patch returns Invalid error", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     200,
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
								field.NewPath("spec", "vcpus"),
								200,
								"spec.vcpus in body should be less than or equal to 128",
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

	It("preserves existing network attachments while patching a lifecycle update", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{Vcpus: 4, MemoryGib: 8}.Build(),
				}.Build(),
			}.Build(), nil)

		existingAttachments := []osacv1alpha1.ComputeNetworkAttachment{{
			SubnetRef:         "stored-subnet-private",
			SecurityGroupRefs: []string{"stored-security-group-private"},
		}}
		existingCR := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   hubNamespace,
				Name:        "vm-existing-network",
				Labels:      map[string]string{labels.ComputeInstanceUuid: computeInstanceID},
				Annotations: map[string]string{"osac.openshift.io/tenant": tenantName},
			},
			Spec: osacv1alpha1.ComputeInstanceSpec{NetworkAttachments: existingAttachments},
		}

		var patched *osacv1alpha1.ComputeInstance
		f, _ := newTestHarness(interceptor.Funcs{
			Patch: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, patch clnt.Patch, opts ...clnt.PatchOption) error {
				if instance, ok := obj.(*osacv1alpha1.ComputeInstance); ok {
					patched = instance.DeepCopy()
				}
				return client.Patch(ctx, obj, patch, opts...)
			},
		}, existingCR)
		var logOutput bytes.Buffer
		f.logger = slog.New(slog.NewTextHandler(&logOutput, nil))

		computeInstance := newComputeInstance()
		restartAt := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
		computeInstance.GetSpec().SetRestartRequestedAt(timestamppb.New(restartAt))
		Expect(f.run(ctx, computeInstance)).To(Succeed())

		Expect(patched).NotTo(BeNil())
		Expect(patched.Spec.NetworkAttachments).To(Equal(existingAttachments))
		Expect(patched.Spec.RestartRequestedAt).NotTo(BeNil())
		Expect(patched.Spec.RestartRequestedAt.Time).To(Equal(restartAt))
		Expect(logOutput.String()).To(ContainSubstring("preserving stored network attachments"))
		Expect(logOutput.String()).NotTo(ContainSubstring("stored-subnet-private"))
		Expect(logOutput.String()).NotTo(ContainSubstring("stored-security-group-private"))
	})

	It("copies resolved network attachments into a newly created CR", func() {
		mockInstanceTypesClient.EXPECT().
			Get(gomock.Any(), gomock.Any()).
			Return(privatev1.InstanceTypesGetResponse_builder{
				Object: privatev1.InstanceType_builder{
					Spec: privatev1.InstanceTypeSpec_builder{Vcpus: 4, MemoryGib: 8}.Build(),
				}.Build(),
			}.Build(), nil)

		f, fakeClient := newTestHarness(interceptor.Funcs{})
		Expect(f.run(ctx, newComputeInstance())).To(Succeed())

		created := &osacv1alpha1.ComputeInstanceList{}
		Expect(fakeClient.List(ctx, created)).To(Succeed())
		Expect(created.Items).To(HaveLen(1))
		Expect(created.Items[0].Spec.NetworkAttachments).To(HaveLen(1))
		Expect(created.Items[0].Spec.NetworkAttachments[0].SubnetRef).To(Equal("test-sn"))
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
						Vcpus:     4,
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
								field.NewPath("spec", "vcpus"),
								150,
								"spec.vcpus in body should be less than or equal to 128",
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
