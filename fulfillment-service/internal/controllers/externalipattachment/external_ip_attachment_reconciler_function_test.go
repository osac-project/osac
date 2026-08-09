/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalipattachment

import (
	"context"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// fakeExternalIPsClient implements the ExternalIPsClient interface for testing selectHub.
type fakeExternalIPsClient struct {
	privatev1.ExternalIPsClient
	getResponse *privatev1.ExternalIPsGetResponse
	getErr      error
}

func (f *fakeExternalIPsClient) Get(
	_ context.Context,
	_ *privatev1.ExternalIPsGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.ExternalIPsGetResponse, error) {
	return f.getResponse, f.getErr
}

// newAttachmentCR creates a typed ExternalIPAttachment CR for use with the fake client.
func newAttachmentCR(id, namespace, name string, deletionTimestamp *metav1.Time) *osacv1alpha1.ExternalIPAttachment {
	obj := &osacv1alpha1.ExternalIPAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.ExternalIPAttachmentUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/externalipattachment"})
	}
	return obj
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the attachment.
func hasFinalizer(attachment *privatev1.ExternalIPAttachment) bool {
	return slices.Contains(attachment.GetMetadata().GetFinalizers(), finalizers.Controller)
}

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(attachmentID, hubID string, hubCache controllers.HubCache) *task {
	attachment := privatev1.ExternalIPAttachment_builder{
		Id: attachmentID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.ExternalIPAttachmentStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:                    f,
		externalIPAttachment: attachment,
	}
}

var _ = Describe("buildSpec", func() {
	It("Includes externalIP and computeInstance in spec", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-1",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-abc123"}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "ci-uuid-abc123"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.ExternalIP).To(Equal("eip-uuid-abc123"))
		Expect(spec.ComputeInstance).ToNot(BeNil())
		Expect(*spec.ComputeInstance).To(Equal("ci-uuid-abc123"))
	})

	It("Handles nil computeInstance", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-2",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-abc456"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.ExternalIP).To(Equal("eip-uuid-abc456"))
		Expect(spec.ComputeInstance).To(BeNil())
	})

	It("Includes cluster and targetEndpoint in spec", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-cluster",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-cluster"}.Build(),
					Cluster:        privatev1.ClusterLocalReference_builder{Id: "cluster-uuid-abc123"}.Build(),
					TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.ExternalIP).To(Equal("eip-uuid-cluster"))
		Expect(spec.Cluster).ToNot(BeNil())
		Expect(*spec.Cluster).To(Equal("cluster-uuid-abc123"))
		Expect(spec.TargetEndpoint).ToNot(BeNil())
		Expect(*spec.TargetEndpoint).To(Equal(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI))
		Expect(spec.ComputeInstance).To(BeNil())
		Expect(spec.BaremetalInstance).To(BeNil())
	})

	It("Includes cluster with ingress endpoint in spec", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-ingress",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-ingress"}.Build(),
					Cluster:        privatev1.ClusterLocalReference_builder{Id: "cluster-uuid-ingress"}.Build(),
					TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.TargetEndpoint).ToNot(BeNil())
		Expect(*spec.TargetEndpoint).To(Equal(osacv1alpha1.ExternalIPAttachmentTargetEndpointIngress))
	})

	It("Includes baremetalInstance in spec", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-bmi",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:        privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-bmi"}.Build(),
					BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: "bmi-uuid-abc123"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.ExternalIP).To(Equal("eip-uuid-bmi"))
		Expect(spec.BaremetalInstance).ToNot(BeNil())
		Expect(*spec.BaremetalInstance).To(Equal("bmi-uuid-abc123"))
		Expect(spec.ComputeInstance).To(BeNil())
		Expect(spec.Cluster).To(BeNil())
		Expect(spec.TargetEndpoint).To(BeNil())
	})

	It("Does not include status fields", func() {
		t := &task{
			externalIPAttachment: privatev1.ExternalIPAttachment_builder{
				Id: "eia-uuid-test-3",
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-abc789"}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "ci-uuid-xyz"}.Build(),
				}.Build(),
				Status: privatev1.ExternalIPAttachmentStatus_builder{
					State:             privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
					Hub:               "hub-1",
					ExternalIpAddress: "10.0.0.5",
				}.Build(),
			}.Build(),
		}

		spec := t.buildSpec()

		Expect(spec.ExternalIP).To(Equal("eip-uuid-abc789"))
	})
})

var _ = Describe("delete", func() {
	const (
		attachmentID = "eia-uuid-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "externalipattachment-test"
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

		t := newTaskForDelete(attachmentID, hubID, hubCache)
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newAttachmentCR(attachmentID, hubNamespace, crName, nil)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

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

		t := newTaskForDelete(attachmentID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newAttachmentCR(attachmentID, hubNamespace, crName, &now)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

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

		t := newTaskForDelete(attachmentID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(attachmentID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(attachmentID, hubID, hubCache)
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())
	})

	It("should remove finalizer when no hub is assigned", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: attachmentID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:                    f,
			externalIPAttachment: attachment,
		}

		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())
	})
})

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "",
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		attachment := privatev1.ExternalIPAttachment_builder{}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-defaults",
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		t.setDefaults()

		Expect(t.externalIPAttachment.GetStatus().GetState()).To(
			Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING),
		)
	})

	It("should not overwrite existing state", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-existing-state",
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		t.setDefaults()

		Expect(t.externalIPAttachment.GetStatus().GetState()).To(
			Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY),
		)
	})

	It("should create status if it doesn't exist", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-no-status",
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		Expect(t.externalIPAttachment.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.externalIPAttachment.HasStatus()).To(BeTrue())
		Expect(t.externalIPAttachment.GetStatus().GetState()).To(
			Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING),
		)
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
		Expect(t.externalIPAttachment.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should create metadata if it doesn't exist", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-no-metadata",
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		Expect(t.externalIPAttachment.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.externalIPAttachment.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		Expect(hasFinalizer(t.externalIPAttachment)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())
		Expect(t.externalIPAttachment.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.externalIPAttachment)).To(BeFalse())
		Expect(t.externalIPAttachment.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-no-metadata",
		}.Build()

		t := &task{
			externalIPAttachment: attachment,
		}

		t.removeFinalizer()

		Expect(t.externalIPAttachment.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("selectHub", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should use existing hub from status", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{
				Namespace: "hub-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-existing-hub",
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-1"}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				Hub: "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:   logger,
			hubCache: hubCache,
		}

		t := &task{
			r:                    f,
			externalIPAttachment: attachment,
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("hub-1"))
		Expect(t.hubNamespace).To(Equal("hub-ns"))
	})

	It("should derive hub from parent ExternalIP when status hub is empty", func() {
		externalIPsClient := &fakeExternalIPsClient{
			getResponse: privatev1.ExternalIPsGetResponse_builder{
				Object: privatev1.ExternalIP_builder{
					Id: "eip-uuid-1",
					Status: privatev1.ExternalIPStatus_builder{
						Hub: "eip-hub-1",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "eip-hub-1").
			Return(&controllers.HubEntry{
				Namespace: "eip-hub-ns",
				Client:    fake.NewClientBuilder().Build(),
			}, nil)

		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-derive-hub",
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-1"}.Build(),
			}.Build(),
		}.Build()

		f := &function{
			logger:            logger,
			hubCache:          hubCache,
			externalIPsClient: externalIPsClient,
		}

		t := &task{
			r:                    f,
			externalIPAttachment: attachment,
		}

		err := t.selectHub(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.hubId).To(Equal("eip-hub-1"))
		Expect(t.hubNamespace).To(Equal("eip-hub-ns"))
	})

	It("should return error when parent ExternalIP has no hub", func() {
		externalIPsClient := &fakeExternalIPsClient{
			getResponse: privatev1.ExternalIPsGetResponse_builder{
				Object: privatev1.ExternalIP_builder{
					Id:     "eip-uuid-no-hub",
					Status: privatev1.ExternalIPStatus_builder{}.Build(),
				}.Build(),
			}.Build(),
		}

		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-eip-no-hub",
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-no-hub"}.Build(),
			}.Build(),
		}.Build()

		f := &function{
			logger:            logger,
			externalIPsClient: externalIPsClient,
		}

		t := &task{
			r:                    f,
			externalIPAttachment: attachment,
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no hub assigned yet"))
	})

	It("should return error when ExternalIP lookup fails", func() {
		externalIPsClient := &fakeExternalIPsClient{
			getErr: errors.New("external IP not found"),
		}

		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-uuid-eip-error",
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-uuid-missing"}.Build(),
			}.Build(),
		}.Build()

		f := &function{
			logger:            logger,
			externalIPsClient: externalIPsClient,
		}

		t := &task{
			r:                    f,
			externalIPAttachment: attachment,
		}

		err := t.selectHub(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("external IP not found"))
	})
})

var _ = Describe("Kubernetes validation error handling", func() {
	It("should set state to FAILED when K8s Create returns Invalid error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ExternalIPAttachment"},
						"eia-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "externalIP"),
								"invalid-value",
								"spec.externalIP is invalid",
							),
						},
					)
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), "hub-1").
			Return(&controllers.HubEntry{Namespace: "test-ns", Client: fakeClient}, nil).
			AnyTimes()

		externalIPAttachmentsClient := NewMockExternalIPAttachmentsClient(ctrl)
		externalIPAttachmentsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ExternalIPAttachmentsUpdateRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsUpdateResponse, error) {
				return &privatev1.ExternalIPAttachmentsUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		attachment := privatev1.ExternalIPAttachment_builder{
			Id: "eia-validation-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "eip-123"}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
				Hub:   "hub-1",
			}.Build(),
		}.Build()

		f := &function{
			logger:                      logger,
			hubCache:                    hubCache,
			externalIPAttachmentsClient: externalIPAttachmentsClient,
			maskCalculator:              masks.NewCalculator().Build(),
		}

		err := f.run(ctx, attachment)
		Expect(err).ToNot(HaveOccurred())

		Expect(attachment.GetStatus().GetState()).To(
			Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED),
		)
		Expect(attachment.GetStatus().GetMessage()).To(ContainSubstring("spec.externalIP"))
	})
})
