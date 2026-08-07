/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstance

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
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
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
)

func newBareMetalInstanceCR(id, namespace, name string, deletionTimestamp *metav1.Time) *bmfov1alpha1.BareMetalInstance {
	obj := &bmfov1alpha1.BareMetalInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				labels.BareMetalInstanceUuid: id,
			},
		},
	}
	if deletionTimestamp != nil {
		obj.SetDeletionTimestamp(deletionTimestamp)
		obj.SetFinalizers([]string{"osac.openshift.io/baremetalinstance"})
	}
	return obj
}

func hasFinalizer(bmi *privatev1.BareMetalInstance) bool {
	return slices.Contains(bmi.GetMetadata().GetFinalizers(), finalizers.Controller)
}

func newTaskForDelete(bmiID, hubID string, hubCache controllers.HubCache) *task {
	bmi := privatev1.BareMetalInstance_builder{
		Id: bmiID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.BareMetalInstanceStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:                 f,
		bareMetalInstance: bmi,
	}
}

func newFakeScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(bmfov1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

var _ = Describe("mutateBMI", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should set TemplateID from catalog item template reference", func() {
		catalogItemID := "catalog-item-1"
		templateID := "osac.templates.gpu_host"

		catalogItemsClient := &fakeCatalogItemsClient{
			getResponse: privatev1.BareMetalInstanceCatalogItemsGetResponse_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Id:       catalogItemID,
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: templateID}.Build(),
				}.Build(),
			}.Build(),
		}

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: catalogItemID},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.TemplateID).To(Equal(templateID))
		Expect(obj.Spec.HostType).To(Equal("default"))
	})

	It("should map run_strategy ALWAYS to RunStrategy Always", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					RunStrategy: new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.RunStrategy).To(Equal(bmfov1alpha1.RunStrategyAlways))
	})

	It("should map run_strategy HALTED to RunStrategy Halted", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					RunStrategy: new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.RunStrategy).To(Equal(bmfov1alpha1.RunStrategyHalted))
	})

	It("should leave RunStrategy empty when run_strategy is not set", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.RunStrategy).To(Equal(bmfov1alpha1.RunStrategyUnspecified))
	})

	It("should propagate restart_trigger to CR spec", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:    &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					RestartTrigger: 42,
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.RestartTrigger).To(Equal(int64(42)))
	})

	It("should leave restart_trigger as zero when not set", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.RestartTrigger).To(Equal(int64(0)))
	})

	It("should include sshPublicKey and userDataSecret in templateParameters", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:  &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey: new("ssh-ed25519 AAAA... test@example.com"),
				}.Build(),
			}.Build(),
			userDataSecretName: "bmi-test-user-data",
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]string
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["sshPublicKey"]).To(Equal("ssh-ed25519 AAAA... test@example.com"))
		Expect(params["userDataSecret"]).To(Equal("bmi-test-user-data"))
	})

	It("should include only sshPublicKey when no user data", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()
		sshPublicKey := "ssh-ed25519 AAAA... test@example.com"

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:  &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey: new(sshPublicKey),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]string
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params).To(HaveKey("sshPublicKey"))
		Expect(params["sshPublicKey"]).To(Equal(sshPublicKey))
		Expect(params).ToNot(HaveKey("userDataSecret"))
	})

	It("should leave templateParameters empty when no ssh_public_key or user_data", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.TemplateParameters).To(BeEmpty())
	})

	It("should include user-provided template_parameters in CR templateParameters", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:        &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["os_version"]).To(Equal("rhel9.4"))
	})

	It("should merge user template_parameters with system parameters", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:        &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey:       new("ssh-ed25519 AAAA... test@example.com"),
					TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
				}.Build(),
			}.Build(),
			userDataSecretName: "bmi-test-user-data",
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["os_version"]).To(Equal("rhel9.4"))
		Expect(params["sshPublicKey"]).To(Equal("ssh-ed25519 AAAA... test@example.com"))
		Expect(params["userDataSecret"]).To(Equal("bmi-test-user-data"))
	})

	It("should let system parameters override user-provided ones", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		userSshParam, err := anypb.New(wrapperspb.String("user-provided-key"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:        &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey:       new("ssh-ed25519 AAAA... real@example.com"),
					TemplateParameters: map[string]*anypb.Any{"sshPublicKey": userSshParam},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["sshPublicKey"]).To(Equal("ssh-ed25519 AAAA... real@example.com"),
			"system sshPublicKey must override user-provided template_parameters value")
	})

	It("should return error when catalog item fetch fails", func() {
		catalogItemsClient := &fakeCatalogItemsClient{
			getError: errors.New("catalog item not found"),
		}

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "missing-catalog"},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get catalog item"))
		Expect(err.Error()).To(ContainSubstring("missing-catalog"))
	})

	It("should include imageURL in templateParameters when image is set", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/org/rhel9:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]string
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["imageURL"]).To(Equal("quay.io/org/rhel9:latest"))
		Expect(params["imageSourceType"]).To(Equal("registry"))
	})

	It("should not include imageURL in templateParameters when image is not set", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:  &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey: new("ssh-ed25519 AAAA... test@example.com"),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]string
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params).ToNot(HaveKey("imageURL"))
		Expect(params).ToNot(HaveKey("imageSourceType"))
	})

	It("should let system imageURL override user-provided template_parameters value", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		userImageParam, err := anypb.New(wrapperspb.String("user-provided-image"))
		Expect(err).ToNot(HaveOccurred())
		userSourceTypeParam, err := anypb.New(wrapperspb.String("user-provided-type"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					TemplateParameters: map[string]*anypb.Any{
						"imageURL":        userImageParam,
						"imageSourceType": userSourceTypeParam,
					},
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/org/rhel9:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["imageURL"]).To(Equal("quay.io/org/rhel9:latest"),
			"system imageURL must override user-provided template_parameters value")
		Expect(params["imageSourceType"]).To(Equal("registry"),
			"system imageSourceType must override user-provided template_parameters value")
	})

	It("should let system imageSourceType override user-provided template_parameters value", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		userSourceTypeParam, err := anypb.New(wrapperspb.String("user-provided-type"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:        &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					TemplateParameters: map[string]*anypb.Any{"imageSourceType": userSourceTypeParam},
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "oci",
						SourceRef:  "quay.io/org/rhel9:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["imageSourceType"]).To(Equal("oci"),
			"system imageSourceType must override user-provided template_parameters value")
	})

	It("should include imageURL alongside sshPublicKey in templateParameters", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:  &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					SshPublicKey: new("ssh-ed25519 AAAA... test@example.com"),
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/org/fedora:latest",
					}.Build(),
				}.Build(),
			}.Build(),
			userDataSecretName: "bmi-test-user-data",
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]string
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params["imageURL"]).To(Equal("quay.io/org/fedora:latest"))
		Expect(params["imageSourceType"]).To(Equal("registry"))
		Expect(params["sshPublicKey"]).To(Equal("ssh-ed25519 AAAA... test@example.com"))
		Expect(params["userDataSecret"]).To(Equal("bmi-test-user-data"))
	})

	It("should copy single network attachment with all fields", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "catalog-1"}.Build(),
					NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{
								privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build(),
								privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build(),
							},
							Interface: new("data-0"),
							Primary:   new(true),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.NetworkAttachments).To(HaveLen(1))
		Expect(obj.Spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-1"))
		Expect(obj.Spec.NetworkAttachments[0].SecurityGroupRefs).To(Equal([]string{"sg-1", "sg-2"}))
		Expect(obj.Spec.NetworkAttachments[0].Interface).To(Equal("data-0"))
		Expect(obj.Spec.NetworkAttachments[0].Primary).To(BeTrue())
	})

	It("should copy multiple network attachments preserving order", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "catalog-1"}.Build(),
					NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-data"}.Build(),
							Interface: new("data-0"),
							Primary:   new(true),
						}.Build(),
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-storage"}.Build(),
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{
								privatev1.SecurityGroupLocalReference_builder{Id: "sg-storage"}.Build(),
							},
							Interface: new("data-1"),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.NetworkAttachments).To(HaveLen(2))
		Expect(obj.Spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-data"))
		Expect(obj.Spec.NetworkAttachments[0].Interface).To(Equal("data-0"))
		Expect(obj.Spec.NetworkAttachments[0].Primary).To(BeTrue())
		Expect(obj.Spec.NetworkAttachments[1].SubnetRef).To(Equal("subnet-storage"))
		Expect(obj.Spec.NetworkAttachments[1].SecurityGroupRefs).To(Equal([]string{"sg-storage"}))
		Expect(obj.Spec.NetworkAttachments[1].Interface).To(Equal("data-1"))
		Expect(obj.Spec.NetworkAttachments[1].Primary).To(BeFalse())
	})

	It("should leave NetworkAttachments empty when proto has none", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "catalog-1"}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.NetworkAttachments).To(BeEmpty())
	})

	It("should handle attachment with optional fields omitted", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "catalog-1"}.Build(),
					NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())
		Expect(obj.Spec.NetworkAttachments).To(HaveLen(1))
		Expect(obj.Spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-1"))
		Expect(obj.Spec.NetworkAttachments[0].SecurityGroupRefs).To(BeEmpty())
		Expect(obj.Spec.NetworkAttachments[0].Interface).To(BeEmpty())
		Expect(obj.Spec.NetworkAttachments[0].Primary).To(BeFalse())
	})

	It("should not include imageSourceType when source_type is empty", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceRef: "quay.io/org/rhel9:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err := t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params).To(HaveKey("imageURL"))
		Expect(params).ToNot(HaveKey("imageSourceType"))
	})

	It("should strip user-provided imageSourceType when image has empty source_type", func() {
		catalogItemsClient := defaultFakeCatalogItemsClient()

		userSourceTypeParam, err := anypb.New(wrapperspb.String("user-injected-type"))
		Expect(err).ToNot(HaveOccurred())

		t := &task{
			r: &function{
				logger:                              logger,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem:        &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
					TemplateParameters: map[string]*anypb.Any{"imageSourceType": userSourceTypeParam},
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceRef: "oci://registry.example.com/rhel9:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}

		var obj bmfov1alpha1.BareMetalInstance
		err = t.mutateBMI(ctx, &obj)
		Expect(err).ToNot(HaveOccurred())

		var params map[string]any
		Expect(json.Unmarshal([]byte(obj.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params).To(HaveKey("imageURL"))
		Expect(params).ToNot(HaveKey("imageSourceType"),
			"user-provided imageSourceType must be stripped when spec image has no source_type")
	})

})

var _ = Describe("update", func() {
	It("should preserve operator-managed spec fields when patching an existing CR", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		const (
			bmiID        = "bmi-test-id"
			hubID        = "hub-1"
			hubNamespace = "test-ns"
		)

		existingCR := &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      "bmi-existing",
				Labels: map[string]string{
					labels.BareMetalInstanceUuid: bmiID,
				},
			},
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				HostType:       "default",
				TemplateID:     "osac.templates.default",
				ExternalHostID: "host-42",
				HostClass:      "openstack",
			},
		}

		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingCR).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		catalogItemsClient := defaultFakeCatalogItemsClient()

		t := &task{
			r: &function{
				logger:                              logger,
				hubCache:                            hubCache,
				bareMetalInstanceCatalogItemsClient: catalogItemsClient,
			},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: bmiID,
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
					Tenant:     "test-tenant",
				}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Id: "catalog-1"},
				}.Build(),
				Status: privatev1.BareMetalInstanceStatus_builder{
					Hub:   hubID,
					State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING,
				}.Build(),
			}.Build(),
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		var updatedCR bmfov1alpha1.BareMetalInstance
		err = fakeClient.Get(ctx, clnt.ObjectKey{
			Namespace: hubNamespace,
			Name:      "bmi-existing",
		}, &updatedCR)
		Expect(err).ToNot(HaveOccurred())
		Expect(updatedCR.Spec.ExternalHostID).To(Equal("host-42"),
			"ExternalHostID must be preserved — it is managed by the bare-metal-fulfillment-operator")
		Expect(updatedCR.Spec.HostClass).To(Equal("openstack"),
			"HostClass must be preserved — it is managed by the bare-metal-fulfillment-operator")
	})
})

var _ = Describe("delete", func() {
	const (
		bmiID        = "test-bmi-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		crName       = "bmi-test"
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
		scheme := newFakeScheme()
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

		t := newTaskForDelete(bmiID, hubID, hubCache)
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeFalse())
	})

	It("should call hubClient.Delete when K8s object exists without DeletionTimestamp", func() {
		cr := newBareMetalInstanceCR(bmiID, hubNamespace, crName, nil)

		scheme := newFakeScheme()

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

		t := newTaskForDelete(bmiID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeTrue())
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeTrue())
	})

	It("should not call hubClient.Delete when K8s object has DeletionTimestamp", func() {
		now := metav1.Now()
		cr := newBareMetalInstanceCR(bmiID, hubNamespace, crName, &now)

		scheme := newFakeScheme()

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

		t := newTaskForDelete(bmiID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteCalled).To(BeFalse())
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeTrue())
	})

	It("should propagate error when hub cache returns error", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, errors.New("hub not found"))

		t := newTaskForDelete(bmiID, hubID, hubCache)

		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hub not found"))
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeTrue())
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(bmiID, hubID, hubCache)
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeTrue())

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeFalse())
	})

	It("should set state to DELETING and READY to False on delete", func() {
		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		t := newTaskForDelete(bmiID, hubID, hubCache)
		t.bareMetalInstance.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING)

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING))
		ready := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY)
		Expect(ready).ToNot(BeNil())
		Expect(ready.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("should remove finalizer when no hub is assigned", func() {
		bmi := privatev1.BareMetalInstance_builder{
			Id: bmiID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.BareMetalInstanceStatus_builder{}.Build(),
		}.Build()

		f := &function{
			logger: logger,
		}

		t := &task{
			r:                 f,
			bareMetalInstance: bmi,
		}

		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasFinalizer(t.bareMetalInstance)).To(BeFalse())
	})
})

var _ = Describe("ensureUserDataSecret", func() {
	const (
		bmiID        = "test-bmi-user-data"
		hubNamespace = "test-ns"
		crName       = "bmi-test"
		crUID        = "test-uid-123"
	)

	var (
		ctx   context.Context
		owner *bmfov1alpha1.BareMetalInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		owner = &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      crName,
				UID:       crUID,
			},
		}
	})

	It("should create a Secret with owner reference, labels, and content", func() {
		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r: &function{logger: logger},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: bmiID,
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserData: new("#cloud-config\npackages:\n  - vim"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: bmiID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())

		secret := &unstructured.Unstructured{}
		secret.SetGroupVersionKind(gvks.Secret)
		err = fakeClient.Get(ctx, clnt.ObjectKey{
			Namespace: hubNamespace,
			Name:      bmiID + userDataSecretSuffix,
		}, secret)
		Expect(err).ToNot(HaveOccurred())

		stringData, found, err := unstructured.NestedMap(secret.Object, "stringData")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(stringData[userDataSecretKey]).To(Equal("#cloud-config\npackages:\n  - vim"))

		Expect(secret.GetLabels()[labels.BareMetalInstanceUuid]).To(Equal(bmiID))

		ownerRefs := secret.GetOwnerReferences()
		Expect(ownerRefs).To(HaveLen(1))
		Expect(ownerRefs[0].Name).To(Equal(crName))
		Expect(ownerRefs[0].UID).To(Equal(owner.GetUID()))
		Expect(ownerRefs[0].Kind).To(Equal("BareMetalInstance"))
	})

	It("should be idempotent when Secret already exists", func() {
		existingSecret := &unstructured.Unstructured{}
		existingSecret.SetGroupVersionKind(gvks.Secret)
		existingSecret.SetNamespace(hubNamespace)
		existingSecret.SetName(bmiID + userDataSecretSuffix)

		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingSecret).
			Build()

		t := &task{
			r: &function{logger: logger},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: bmiID,
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserData: new("some-data"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: bmiID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should propagate error when Secret creation fails", func() {
		scheme := newFakeScheme()
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
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: bmiID,
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserData: new("some-data"),
				}.Build(),
			}.Build(),
			hubNamespace:       hubNamespace,
			hubClient:          fakeClient,
			userDataSecretName: bmiID + userDataSecretSuffix,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("create failed"))
	})

	It("should not create a Secret when userDataSecretName is empty", func() {
		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		t := &task{
			r: &function{logger: logger},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id:   bmiID,
				Spec: privatev1.BareMetalInstanceSpec_builder{}.Build(),
			}.Build(),
			hubNamespace: hubNamespace,
			hubClient:    fakeClient,
		}

		err := t.ensureUserDataSecret(ctx, owner)
		Expect(err).ToNot(HaveOccurred())
	})
})

func findProtoCondition(bmi *privatev1.BareMetalInstance, condType privatev1.BareMetalInstanceConditionType) *privatev1.BareMetalInstanceCondition {
	for _, c := range bmi.GetStatus().GetConditions() {
		if c.GetType() == condType {
			return c
		}
	}
	return nil
}

var _ = Describe("syncStatus", func() {
	newTask := func(specRestartTrigger int64) *task {
		return &task{
			r: &function{logger: logger},
			bareMetalInstance: privatev1.BareMetalInstance_builder{
				Id: "bmi-sync-test",
				Spec: privatev1.BareMetalInstanceSpec_builder{
					RestartTrigger: specRestartTrigger,
				}.Build(),
				Status: privatev1.BareMetalInstanceStatus_builder{
					State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING,
				}.Build(),
			}.Build(),
		}
	}

	It("should not change state when object is nil", func() {
		t := newTask(0)
		t.syncStatus(nil)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING))
	})

	It("should not change state when phase is empty", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING))
	})

	It("should map Allocating phase to PROVISIONING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseAllocating,
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING))
	})

	It("should map Progressing phase to PROVISIONING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseProgressing,
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING))
	})

	It("should map Ready phase with PowerOn condition to RUNNING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionTrue,
						Reason: bmfov1alpha1.HostConditionReasonPowerOn,
					},
				},
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING))
	})

	It("should map Ready phase with PowerOff condition to STOPPED state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionTrue,
						Reason: bmfov1alpha1.HostConditionReasonPowerOff,
					},
				},
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED))
	})

	It("should map Progressing phase with PowerSynced Progressing and RunStrategy Always to STARTING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				RunStrategy: bmfov1alpha1.RunStrategyAlways,
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseProgressing,
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  bmfov1alpha1.HostConditionReasonProgressing,
						Message: "node power state is transitioning",
					},
				},
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STARTING))
	})

	It("should map Progressing phase with PowerSynced Progressing and RunStrategy Halted to STOPPING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				RunStrategy: bmfov1alpha1.RunStrategyHalted,
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseProgressing,
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  bmfov1alpha1.HostConditionReasonProgressing,
						Message: "node power state is transitioning",
					},
				},
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING))
	})

	It("should map PowerSynced=False with non-Progressing reason to FAILED state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionFalse,
						Reason: bmfov1alpha1.HostConditionReasonIronicAPIFailure,
					},
				},
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED))
	})

	It("should map Failed phase to FAILED state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseFailed,
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED))
	})

	It("should map Deleting phase to DELETING state", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseDeleting,
			},
		}
		t.syncStatus(object)
		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING))
	})

	It("should set PROVISIONED=True when ProvisionTemplateComplete is True", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				TemplateID: "some_template",
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionProvisionTemplateComplete),
						Status: metav1.ConditionTrue,
						Reason: "Succeeded",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should not set PROVISIONED=True when ProvisionTemplateComplete is absent", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(cond.GetStatus()).ToNot(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should not set PROVISIONED=True when ProvisionTemplateComplete=False", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionProvisionTemplateComplete),
						Status: metav1.ConditionFalse,
						Reason: "Progressing",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(cond.GetStatus()).ToNot(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should not demote PROVISIONED to False when ProvisionTemplateComplete goes False (ratchet)", func() {
		t := newTask(0)
		t.updateCondition(
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "", "")

		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionAllocated),
						Status: metav1.ConditionTrue,
						Reason: "Allocated",
					},
					{
						Type:   string(bmfov1alpha1.HostConditionProvisionTemplateComplete),
						Status: metav1.ConditionFalse,
						Reason: "Progressing",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should map ProvisionTemplateComplete condition to CONFIGURATION_APPLIED with sanitized message", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionProvisionTemplateComplete),
						Status:  metav1.ConditionTrue,
						Reason:  "Complete",
						Message: "Provision template completed",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetMessage()).To(Equal("Configuration successfully applied"))
	})

	DescribeTable("should set READY=False when state is PROVISIONING, DELETING, or FAILED",
		func(phase bmfov1alpha1.BareMetalInstancePhaseType) {
			t := newTask(0)
			object := &bmfov1alpha1.BareMetalInstance{
				Status: bmfov1alpha1.BareMetalInstanceStatus{Phase: phase},
			}
			t.syncStatus(object)
			cond := findProtoCondition(t.bareMetalInstance,
				privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY)
			Expect(cond).ToNot(BeNil())
			Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		},
		Entry("Allocating → PROVISIONING", bmfov1alpha1.BareMetalInstancePhaseAllocating),
		Entry("Progressing → PROVISIONING", bmfov1alpha1.BareMetalInstancePhaseProgressing),
		Entry("Deleting → DELETING", bmfov1alpha1.BareMetalInstancePhaseDeleting),
		Entry("Failed → FAILED", bmfov1alpha1.BareMetalInstancePhaseFailed),
	)

	DescribeTable("should set READY=True when state is RUNNING or STOPPED",
		func(phase bmfov1alpha1.BareMetalInstancePhaseType, cond metav1.Condition) {
			t := newTask(0)
			object := &bmfov1alpha1.BareMetalInstance{
				Status: bmfov1alpha1.BareMetalInstanceStatus{
					Phase:      phase,
					Conditions: []metav1.Condition{cond},
				},
			}
			t.syncStatus(object)
			ready := findProtoCondition(t.bareMetalInstance,
				privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY)
			Expect(ready).ToNot(BeNil())
			Expect(ready.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		},
		Entry("Ready+PowerOn → RUNNING", bmfov1alpha1.BareMetalInstancePhaseReady,
			metav1.Condition{Type: string(bmfov1alpha1.HostConditionPowerSynced), Status: metav1.ConditionTrue, Reason: bmfov1alpha1.HostConditionReasonPowerOn}),
		Entry("Ready+PowerOff → STOPPED", bmfov1alpha1.BareMetalInstancePhaseReady,
			metav1.Condition{Type: string(bmfov1alpha1.HostConditionPowerSynced), Status: metav1.ConditionTrue, Reason: bmfov1alpha1.HostConditionReasonPowerOff}),
	)

	It("should set RESTART_IN_PROGRESS when PowerSynced is False with Progressing reason and restart is pending", func() {
		t := newTask(42)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  bmfov1alpha1.HostConditionReasonProgressing,
						Message: "Power cycle in progress",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetMessage()).To(Equal("Restart in progress"))
	})

	It("should not set RESTART_IN_PROGRESS when PowerSynced is False with Progressing reason but no restart pending", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  bmfov1alpha1.HostConditionReasonProgressing,
						Message: "node power state is transitioning",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(cond).To(BeNil())
	})

	DescribeTable("should set RESTART_FAILED with PowerSyncFailed reason for any failure when restart is pending",
		func(reason string) {
			t := newTask(42)
			object := &bmfov1alpha1.BareMetalInstance{
				Status: bmfov1alpha1.BareMetalInstanceStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(bmfov1alpha1.HostConditionPowerSynced),
							Status:  metav1.ConditionFalse,
							Reason:  reason,
							Message: "something went wrong",
						},
					},
				},
			}
			t.syncStatus(object)
			cond := findProtoCondition(t.bareMetalInstance,
				privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
			Expect(cond).ToNot(BeNil())
			Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			Expect(cond.GetReason()).To(Equal("PowerSyncFailed"))
		},
		Entry("IronicAPIFailure", bmfov1alpha1.HostConditionReasonIronicAPIFailure),
		Entry("unknown failure reason", "SomeOtherFailure"),
	)

	It("should not set RESTART_FAILED when PowerSynced fails but no restart pending", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  bmfov1alpha1.HostConditionReasonIronicAPIFailure,
						Message: "backend API call failed",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(cond).To(BeNil())
	})

	It("should clear RESTART_FAILED when RESTART_IN_PROGRESS becomes True", func() {
		t := newTask(42)
		t.updateCondition(
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "PowerSyncFailed", "Restart failed")

		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionFalse,
						Reason: bmfov1alpha1.HostConditionReasonProgressing,
					},
				},
			},
		}
		t.syncStatus(object)

		inProgress := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(inProgress.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

		failed := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(failed.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("should clear RESTART_IN_PROGRESS when RESTART_FAILED becomes True", func() {
		t := newTask(42)
		t.updateCondition(
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "Progressing", "Restart in progress")

		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionFalse,
						Reason: bmfov1alpha1.HostConditionReasonIronicAPIFailure,
					},
				},
			},
		}
		t.syncStatus(object)

		failed := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(failed.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

		inProgress := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(inProgress.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("should clear restart conditions and sync restart trigger when PowerSynced is True", func() {
		t := newTask(42)
		object := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				RestartTrigger: 42,
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				RestartTrigger: 42,
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionTrue,
						Reason:  bmfov1alpha1.HostConditionReasonPowerOn,
						Message: "Power on complete",
					},
				},
			},
		}
		t.syncStatus(object)

		inProgress := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(inProgress).ToNot(BeNil())
		Expect(inProgress.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))

		failed := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(failed).ToNot(BeNil())
		Expect(failed.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))

		Expect(t.bareMetalInstance.GetStatus().GetRestartTrigger()).To(Equal(int64(42)))
	})

	It("should not echo restart trigger when operator has not completed restart", func() {
		t := newTask(42)
		object := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				RestartTrigger: 42,
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				RestartTrigger: 0,
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionTrue,
						Reason:  bmfov1alpha1.HostConditionReasonPowerOn,
						Message: "Power on complete",
					},
				},
			},
		}
		t.syncStatus(object)

		Expect(t.bareMetalInstance.GetStatus().GetRestartTrigger()).To(Equal(int64(0)))

		inProgress := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(inProgress).ToNot(BeNil())
		Expect(inProgress.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

		failed := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(failed).ToNot(BeNil())
		Expect(failed.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("should not set restart conditions when PowerSynced is False with unhandled reason", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionPowerSynced),
						Status:  metav1.ConditionFalse,
						Reason:  "UnknownReason",
						Message: "Something unexpected",
					},
				},
			},
		}
		t.syncStatus(object)

		inProgress := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS)
		Expect(inProgress).To(BeNil())

		failed := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED)
		Expect(failed).To(BeNil())
	})

	It("should map multiple conditions in a single call", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				Conditions: []metav1.Condition{
					{
						Type:   string(bmfov1alpha1.HostConditionAllocated),
						Status: metav1.ConditionTrue,
						Reason: "HostAllocated",
					},
					{
						Type:   string(bmfov1alpha1.HostConditionProvisionTemplateComplete),
						Status: metav1.ConditionTrue,
						Reason: "Complete",
					},
					{
						Type:   string(bmfov1alpha1.HostConditionAvailable),
						Status: metav1.ConditionTrue,
						Reason: "HostAvailable",
					},
					{
						Type:   string(bmfov1alpha1.HostConditionPowerSynced),
						Status: metav1.ConditionTrue,
						Reason: bmfov1alpha1.HostConditionReasonPowerOn,
					},
				},
			},
		}
		t.syncStatus(object)

		Expect(t.bareMetalInstance.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING))

		provisioned := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(provisioned).ToNot(BeNil())
		Expect(provisioned.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

		configApplied := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED)
		Expect(configApplied).ToNot(BeNil())
		Expect(configApplied.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

		ready := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY)
		Expect(ready).ToNot(BeNil())
		Expect(ready.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should not set PROVISIONED when only Allocated is True (template not yet complete)", func() {
		t := newTask(0)
		object := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(bmfov1alpha1.HostConditionAllocated),
						Status:  metav1.ConditionTrue,
						Reason:  "Allocated",
						Message: "BareMetalInstance allocated a host (osac/fake-bm-host-2) from metal3",
					},
				},
			},
		}
		t.syncStatus(object)
		cond := findProtoCondition(t.bareMetalInstance,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED)
		Expect(cond.GetStatus()).ToNot(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})
})

var _ = Describe("sanitizeConditionMessage", func() {
	It("should return tenant-facing message for Allocated condition when True", func() {
		msg := sanitizeConditionMessage(bmfov1alpha1.HostConditionAllocated, metav1.ConditionTrue)
		Expect(msg).To(Equal("BareMetalInstance successfully provisioned"))
	})

	It("should return tenant-facing message for ProvisionTemplateComplete condition when True", func() {
		msg := sanitizeConditionMessage(bmfov1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionTrue)
		Expect(msg).To(Equal("Configuration successfully applied"))
	})

	It("should return tenant-facing message for Available condition when True", func() {
		msg := sanitizeConditionMessage(bmfov1alpha1.HostConditionAvailable, metav1.ConditionTrue)
		Expect(msg).To(Equal("BareMetalInstance is ready"))
	})

	It("should return empty message for conditions with False status", func() {
		msg := sanitizeConditionMessage(bmfov1alpha1.HostConditionAllocated, metav1.ConditionFalse)
		Expect(msg).To(BeEmpty())
	})
})

var _ = Describe("mapConditionStatus", func() {
	It("should map ConditionTrue to CONDITION_STATUS_TRUE", func() {
		Expect(mapConditionStatus(metav1.ConditionTrue)).To(
			Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("should map ConditionFalse to CONDITION_STATUS_FALSE", func() {
		Expect(mapConditionStatus(metav1.ConditionFalse)).To(
			Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("should map ConditionUnknown to CONDITION_STATUS_UNSPECIFIED", func() {
		Expect(mapConditionStatus(metav1.ConditionUnknown)).To(
			Equal(privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED))
	})
})

func defaultFakeCatalogItemsClient() *fakeCatalogItemsClient {
	return &fakeCatalogItemsClient{
		getResponse: privatev1.BareMetalInstanceCatalogItemsGetResponse_builder{
			Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Template: &privatev1.BareMetalInstanceTemplateReference{Name: "osac.templates.default"},
			}.Build(),
		}.Build(),
	}
}

// fakeCatalogItemsClient is a simple test double for the BareMetalInstanceCatalogItemsClient.
type fakeCatalogItemsClient struct {
	privatev1.BareMetalInstanceCatalogItemsClient
	getResponse *privatev1.BareMetalInstanceCatalogItemsGetResponse
	getError    error
}

func (c *fakeCatalogItemsClient) Get(ctx context.Context, req *privatev1.BareMetalInstanceCatalogItemsGetRequest, opts ...grpc.CallOption) (*privatev1.BareMetalInstanceCatalogItemsGetResponse, error) {
	return c.getResponse, c.getError
}

var _ = Describe("Kubernetes validation error handling", func() {
	It("should set state to FAILED when K8s Create returns Invalid error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		const (
			bmiID        = "bmi-invalid-test"
			hubID        = "hub-1"
			hubNamespace = "test-ns"
		)

		scheme := newFakeScheme()
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "bmfo.osac.openshift.io", Kind: "BareMetalInstance"},
						"bmi-test",
						field.ErrorList{field.Invalid(field.NewPath("spec", "templateID"), "", "invalid template")},
					)
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		bareMetalInstancesClient := NewMockBareMetalInstancesClient(ctrl)
		bareMetalInstancesClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.BareMetalInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.BareMetalInstancesUpdateResponse, error) {
				return &privatev1.BareMetalInstancesUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		bmi := privatev1.BareMetalInstance_builder{
			Id: bmiID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "catalog-1"}.Build(),
			}.Build(),
			Status: privatev1.BareMetalInstanceStatus_builder{
				Hub: hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:                              logger,
			hubCache:                            hubCache,
			bareMetalInstancesClient:            bareMetalInstancesClient,
			bareMetalInstanceCatalogItemsClient: defaultFakeCatalogItemsClient(),
			maskCalculator:                      masks.NewCalculator().Build(),
		}

		err := f.run(ctx, bmi)
		Expect(err).ToNot(HaveOccurred())

		Expect(bmi.GetStatus().GetState()).To(
			Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED),
		)

		cond := findProtoCondition(bmi,
			privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ValidationFailed"))
		Expect(cond.GetMessage()).To(ContainSubstring("invalid template"))
	})
})
