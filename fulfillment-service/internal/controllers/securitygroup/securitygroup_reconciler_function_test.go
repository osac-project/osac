/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package securitygroup

import (
	"context"
	"errors"
	"slices"
	"time"

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

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkingHubReader struct {
	result controllers.NetworkingHubResolution
	err    error
	calls  int
}

func (f *fakeNetworkingHubReader) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	f.calls++
	return f.result, f.err
}

func readyNetworkingHubReader(id, namespace string, client clnt.Client) *fakeNetworkingHubReader {
	return &fakeNetworkingHubReader{result: controllers.NetworkingHubResolution{
		NetworkingHub: controllers.NetworkingHub{ID: id, Namespace: namespace, Client: client},
		HubID:         id,
		State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
	}}
}

var _ = Describe("buildSpec", func() {
	It("Includes virtualNetwork and rules", func() {
		portFrom := int32(80)
		portTo := int32(443)
		ipv4 := "10.0.0.0/8"

		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-test-123",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-123"}.Build(),
					Ingress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							PortFrom: &portFrom,
							PortTo:   &portTo,
							Ipv4Cidr: &ipv4,
						}.Build(),
					},
					Egress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ALL,
							Ipv4Cidr: &ipv4,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		spec, err := t.buildSpec()
		Expect(err).ToNot(HaveOccurred())

		Expect(spec.VirtualNetwork).To(Equal("vnet-123"))

		Expect(spec.IngressRules).To(HaveLen(1))
		Expect(string(spec.IngressRules[0].Protocol)).To(Equal("tcp"))
		Expect(*spec.IngressRules[0].PortFrom).To(Equal(int32(80)))
		Expect(*spec.IngressRules[0].PortTo).To(Equal(int32(443)))
		Expect(spec.IngressRules[0].SourceCIDR).To(Equal("10.0.0.0/8"))
		Expect(spec.IngressRules[0].DestinationCIDR).To(BeEmpty())

		Expect(spec.EgressRules).To(HaveLen(1))
		Expect(string(spec.EgressRules[0].Protocol)).To(Equal("all"))
		Expect(spec.EgressRules[0].DestinationCIDR).To(Equal("10.0.0.0/8"))
		Expect(spec.EgressRules[0].SourceCIDR).To(BeEmpty())
		Expect(spec.EgressRules[0].PortFrom).To(BeNil())
		Expect(spec.EgressRules[0].PortTo).To(BeNil())
	})

	It("Omits empty rule lists", func() {
		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-test-456",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-456"}.Build(),
				}.Build(),
			}.Build(),
		}

		spec, err := t.buildSpec()
		Expect(err).ToNot(HaveOccurred())

		Expect(spec.VirtualNetwork).To(Equal("vnet-456"))
		Expect(spec.IngressRules).To(BeEmpty())
		Expect(spec.EgressRules).To(BeEmpty())
	})

	It("Does not project an IPv6-only ingress rule", func() {
		ipv6 := "2001:db8::/32"
		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-ipv6-only",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-ipv6"}.Build(),
					Ingress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							Ipv6Cidr: &ipv6,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		spec, err := t.buildSpec()
		Expect(err).To(MatchError("security group ingress rule 0 must contain an IPv4 CIDR and no IPv6 CIDR"))

		Expect(spec.IngressRules).To(BeEmpty())
	})

	It("Does not project an IPv6-only egress rule", func() {
		ipv6 := "2001:db8::/32"
		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-ipv6-egress-only",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-ipv6-egress"}.Build(),
					Egress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ALL,
							Ipv6Cidr: &ipv6,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		spec, err := t.buildSpec()
		Expect(err).To(MatchError("security group egress rule 0 must contain an IPv4 CIDR and no IPv6 CIDR"))

		Expect(spec.EgressRules).To(BeEmpty())
	})

	It("Rejects a dual-stack ingress rule instead of silently dropping IPv6", func() {
		ipv4 := "10.0.0.0/8"
		ipv6 := "2001:db8::/32"
		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-dual-stack",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-dual-stack"}.Build(),
					Ingress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							Ipv4Cidr: &ipv4,
							Ipv6Cidr: &ipv6,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		_, err := t.buildSpec()
		Expect(err).To(MatchError("security group ingress rule 0 must contain an IPv4 CIDR and no IPv6 CIDR"))
	})

	It("Rejects an ingress rule with an empty IPv4 CIDR", func() {
		ipv4 := ""
		t := &task{
			securityGroup: privatev1.SecurityGroup_builder{
				Id: "sg-empty-cidr",
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-empty-cidr"}.Build(),
					Ingress: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ALL,
							Ipv4Cidr: &ipv4,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}

		_, err := t.buildSpec()
		Expect(err).To(MatchError("security group ingress rule 0 must contain an IPv4 CIDR and no IPv6 CIDR"))
	})
})

var _ = Describe("protocolToString", func() {
	It("Converts all protocol values correctly", func() {
		Expect(protocolToString(privatev1.Protocol_PROTOCOL_TCP)).To(Equal("tcp"))
		Expect(protocolToString(privatev1.Protocol_PROTOCOL_UDP)).To(Equal("udp"))
		Expect(protocolToString(privatev1.Protocol_PROTOCOL_ICMP)).To(Equal("icmp"))
		Expect(protocolToString(privatev1.Protocol_PROTOCOL_ALL)).To(Equal("all"))
	})
})

// hasFinalizer checks if the fulfillment-controller finalizer is present on the security group.
func hasFinalizer(sg *privatev1.SecurityGroup) bool {
	return slices.Contains(sg.GetMetadata().GetFinalizers(), finalizers.Controller)
}

func expectDeletionRetry(err error) {
	var retryable interface{ RequeueAfter() time.Duration }
	Expect(errors.As(err, &retryable)).To(BeTrue())
	Expect(retryable.RequeueAfter()).To(Equal(time.Second))
	var backoff interface{ UseExponentialBackoff() bool }
	Expect(errors.As(err, &backoff)).To(BeTrue())
	Expect(backoff.UseExponentialBackoff()).To(BeFalse())
}

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		sg := privatev1.SecurityGroup_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		sg := privatev1.SecurityGroup_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "",
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		sg := privatev1.SecurityGroup_builder{}.Build()

		t := &task{
			securityGroup: sg,
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("setDefaults", func() {
	It("should set PENDING state when status is unspecified", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-defaults",
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		t.setDefaults()

		Expect(t.securityGroup.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
	})

	It("should not overwrite existing state", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-existing-state",
			Status: privatev1.SecurityGroupStatus_builder{
				State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		t.setDefaults()

		Expect(t.securityGroup.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
	})

	It("should create status if it doesn't exist", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-no-status",
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		Expect(t.securityGroup.HasStatus()).To(BeFalse())

		t.setDefaults()

		Expect(t.securityGroup.HasStatus()).To(BeTrue())
		Expect(t.securityGroup.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
	})
})

var _ = Describe("addFinalizer", func() {
	It("should add finalizer when not present", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(hasFinalizer(t.securityGroup)).To(BeTrue())
	})

	It("should not add finalizer when already present", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		added := t.addFinalizer()

		Expect(added).To(BeFalse())
		Expect(hasFinalizer(t.securityGroup)).To(BeTrue())
		// Should not duplicate
		Expect(t.securityGroup.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should create metadata if it doesn't exist", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-no-metadata",
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		Expect(t.securityGroup.HasMetadata()).To(BeFalse())

		added := t.addFinalizer()

		Expect(added).To(BeTrue())
		Expect(t.securityGroup.HasMetadata()).To(BeTrue())
		Expect(hasFinalizer(t.securityGroup)).To(BeTrue())
	})
})

var _ = Describe("delete", func() {
	const (
		sgID         = "sg-delete-id"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
		vnetID       = "vnet-123"
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

	It("should remove finalizer for a legacy resource when the canonical Hub is not found", func() {

		resolver := &fakeNetworkingHubReader{
			result: controllers.NetworkingHubResolution{HubID: hubID},
			err:    controllers.ErrCanonicalHubNotFound,
		}

		sg := privatev1.SecurityGroup_builder{
			Id: sgID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnetID}.Build(),
			}.Build(),
		}.Build()

		f := &function{
			logger:              logger,
			networkingHubReader: resolver,
		}

		t := &task{
			r:             f,
			securityGroup: sg,
		}

		Expect(hasFinalizer(t.securityGroup)).To(BeTrue())

		err := t.delete(ctx)
		// Should return nil (not propagate the error)
		Expect(err).ToNot(HaveOccurred())
		// Finalizer should be removed to allow archiving
		Expect(hasFinalizer(t.securityGroup)).To(BeFalse())
	})

	It("should remove finalizer when its assigned Hub has been decommissioned", func() {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(ctx, hubID).Return(nil, controllers.ErrHubNotFound)
		sg := privatev1.SecurityGroup_builder{
			Id: sgID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{Hub: hubID}.Build(),
		}.Build()
		t := &task{
			r:             &function{logger: logger, hubCache: hubCache},
			securityGroup: sg,
		}

		Expect(t.delete(ctx)).To(Succeed())
		Expect(hasFinalizer(sg)).To(BeFalse())
	})

	It("should delete the Kubernetes object from its assigned Hub after the canonical Hub changes", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		oldHubClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "securitygroup-old",
					Namespace: "old-hub-ns",
					Labels:    map[string]string{labels.SecurityGroupUuid: sgID},
				},
			}).
			Build()
		newHubClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		resolver := readyNetworkingHubReader("new-hub", "new-hub-ns", newHubClient)
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().Get(ctx, "old-hub").Return(&controllers.HubEntry{
			Namespace: "old-hub-ns",
			Client:    oldHubClient,
		}, nil)

		sg := privatev1.SecurityGroup_builder{
			Id: sgID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{Hub: "old-hub"}.Build(),
		}.Build()
		f := &function{
			logger:              logger,
			networkingHubReader: resolver,
			hubCache:            hubCache,
		}
		t := &task{r: f, securityGroup: sg}

		err := t.delete(ctx)
		expectDeletionRetry(err)
		Expect(resolver.calls).To(BeZero())

		remaining := &osacv1alpha1.SecurityGroupList{}
		Expect(oldHubClient.List(ctx, remaining, clnt.InNamespace("old-hub-ns"))).To(Succeed())
		Expect(remaining.Items).To(BeEmpty())
		Expect(hasFinalizer(sg)).To(BeTrue())
	})
})

var _ = Describe("removeFinalizer", func() {
	It("should remove finalizer when present", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-has-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		Expect(hasFinalizer(t.securityGroup)).To(BeTrue())

		t.removeFinalizer()

		Expect(hasFinalizer(t.securityGroup)).To(BeFalse())
		// Other finalizers should remain
		Expect(t.securityGroup.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when finalizer not present", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-no-finalizer",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		Expect(hasFinalizer(t.securityGroup)).To(BeFalse())

		t.removeFinalizer()

		Expect(hasFinalizer(t.securityGroup)).To(BeFalse())
		Expect(t.securityGroup.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should do nothing when metadata doesn't exist", func() {
		sg := privatev1.SecurityGroup_builder{
			Id: "sg-no-metadata",
		}.Build()

		t := &task{
			securityGroup: sg,
		}

		// Should not panic
		t.removeFinalizer()

		Expect(t.securityGroup.HasMetadata()).To(BeFalse())
	})
})

var _ = Describe("canonical networking Hub resolution", func() {
	It("uses the canonical Hub repeatedly and does not fall back when it is unavailable", func() {
		kubeClient := fake.NewClientBuilder().Build()
		resolver := readyNetworkingHubReader("hub-a", "hub-a-ns", kubeClient)
		r := &function{logger: logger, networkingHubReader: resolver}
		t := &task{r: r, securityGroup: privatev1.SecurityGroup_builder{}.Build()}
		Expect(t.selectHub(context.Background())).To(Succeed())
		Expect(t.hubId).To(Equal("hub-a"))
		Expect(t.hubNamespace).To(Equal("hub-a-ns"))
		Expect(t.hubClient).To(BeIdenticalTo(kubeClient))
		Expect(t.selectHub(context.Background())).To(Succeed())
		Expect(resolver.calls).To(Equal(2))

		for _, resolutionErr := range []error{
			controllers.ErrNoNetworkingHubs,
			controllers.ErrMultipleNetworkingHubs,
			controllers.ErrCanonicalHubUnavailable,
		} {
			resolver.err = resolutionErr
			failed := &task{r: r, securityGroup: privatev1.SecurityGroup_builder{}.Build()}
			Expect(failed.selectHub(context.Background())).To(MatchError(resolutionErr))
			Expect(failed.hubClient).To(BeNil())
		}
	})

	It("preserves the stored Hub assignment and rejects a changed canonical Hub", func() {
		resolver := readyNetworkingHubReader("new-hub", "new-hub-ns", fake.NewClientBuilder().Build())
		t := &task{
			r: &function{logger: logger, networkingHubReader: resolver},
			securityGroup: privatev1.SecurityGroup_builder{
				Status: privatev1.SecurityGroupStatus_builder{Hub: "old-hub"}.Build(),
			}.Build(),
		}

		Expect(t.selectHub(context.Background())).To(MatchError(ContainSubstring(controllers.ErrResourceHubConflict.Error())))
		Expect(t.hubId).To(Equal("new-hub"))
		Expect(t.securityGroup.GetStatus().GetHub()).To(Equal("old-hub"))
	})

	It("persists the Hub assignment before creating the Kubernetes object", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		resolver := readyNetworkingHubReader("hub-a", "hub-a-ns", kubeClient)
		securityGroupsClient := NewMockSecurityGroupsClient(ctrl)
		securityGroupsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *privatev1.SecurityGroupsUpdateRequest, _ ...grpc.CallOption) (*privatev1.SecurityGroupsUpdateResponse, error) {
				Expect(req.GetObject().GetStatus().GetHub()).To(Equal("hub-a"))
				Expect(req.GetUpdateMask().GetPaths()).To(ContainElement("status.hub"))
				return &privatev1.SecurityGroupsUpdateResponse{Object: req.GetObject()}, nil
			})

		sg := privatev1.SecurityGroup_builder{
			Id: "sg-hub-assignment",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{
				State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
			}.Build(),
		}.Build()
		f := &function{
			logger:               logger,
			securityGroupsClient: securityGroupsClient,
			networkingHubReader:  resolver,
			maskCalculator:       masks.NewCalculator().Build(),
		}

		Expect(f.run(ctx, sg)).To(Succeed())
		Expect(sg.GetStatus().GetHub()).To(Equal("hub-a"))
		objects := &osacv1alpha1.SecurityGroupList{}
		Expect(kubeClient.List(ctx, objects, clnt.InNamespace("hub-a-ns"))).To(Succeed())
		Expect(objects.Items).To(BeEmpty())
	})
})

var _ = Describe("Kubernetes validation error handling", func() {
	It("should mark a legacy IPv6-only rule as failed without contacting Kubernetes", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		ipv6 := "2001:db8::/32"
		resolver := readyNetworkingHubReader("hub-1", "test-ns", nil)

		sg := privatev1.SecurityGroup_builder{
			Id: "sg-legacy-ipv6",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				Ingress: []*privatev1.SecurityRule{
					privatev1.SecurityRule_builder{Ipv6Cidr: &ipv6}.Build(),
				},
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{
				State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:              logger,
				networkingHubReader: resolver,
			},
			securityGroup: sg,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(sg.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))
		Expect(sg.GetStatus().GetMessage()).To(ContainSubstring("IPv4 CIDR and no IPv6 CIDR"))
	})

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
						schema.GroupKind{Group: "osac.openshift.io", Kind: "SecurityGroup"},
						"sg-test",
						field.ErrorList{
							field.Invalid(
								field.NewPath("spec", "virtualNetwork"),
								"invalid-value",
								"spec.virtualNetwork is invalid",
							),
						},
					)
				},
			}).
			Build()

		resolver := readyNetworkingHubReader("hub-1", "test-ns", fakeClient)

		securityGroupsClient := NewMockSecurityGroupsClient(ctrl)
		securityGroupsClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.SecurityGroupsUpdateRequest, opts ...grpc.CallOption) (*privatev1.SecurityGroupsUpdateResponse, error) {
				return &privatev1.SecurityGroupsUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		sg := privatev1.SecurityGroup_builder{
			Id: "sg-validation-test",
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     "test-tenant",
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{
				State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
			}.Build(),
		}.Build()

		f := &function{
			logger:               logger,
			securityGroupsClient: securityGroupsClient,
			networkingHubReader:  resolver,
			maskCalculator:       masks.NewCalculator().Build(),
		}

		// The first pass stores the Hub assignment. The next pass creates the Kubernetes object.
		Expect(f.run(ctx, sg)).To(Succeed())
		err := f.run(ctx, sg)
		Expect(err).ToNot(HaveOccurred())

		Expect(sg.GetStatus().GetState()).To(
			Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED),
		)
		Expect(sg.GetStatus().GetMessage()).To(ContainSubstring("spec.virtualNetwork"))
	})
})
