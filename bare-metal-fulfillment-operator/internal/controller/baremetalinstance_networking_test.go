/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const testNetworkingNS = "osac-networking-test"

type readTrackingClient struct {
	client.Client
	getCalls  int
	listCalls int
}

func (c *readTrackingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.getCalls++
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *readTrackingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.listCalls++
	return c.Client.List(ctx, list, opts...)
}

type readTrackingReader struct {
	client.Reader
	getCalls  int
	listCalls int
}

func (r *readTrackingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	r.getCalls++
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r *readTrackingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	r.listCalls++
	return r.Reader.List(ctx, list, opts...)
}

func forceDeleteNetworkAttachment(ctx context.Context, namespace, name string) {
	na := &opv1alpha1.NetworkAttachment{}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := k8sClient.Get(ctx, key, na); err != nil {
		return
	}
	na.Finalizers = nil
	_ = k8sClient.Update(ctx, na)
	_ = k8sClient.Delete(ctx, na)
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, key, &opv1alpha1.NetworkAttachment{}))
	}).Should(BeTrue())
}

var _ = Describe("BareMetalInstance Networking", func() {
	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
		invClient  *mockInventoryClient
		apiID      string
	)

	bmiWithAttachments := func(id string, attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-net-",
				Namespace:    "default",
				Labels: map[string]string{
					BareMetalInstanceUUIDLabel: id,
				},
				Annotations: map[string]string{
					BareMetalInstanceTenantAnnotation: "tenant-a",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID: "host-123",
				Selector: v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"test": "value"},
				},
				HostClass:          "openstack",
				TemplateID:         "noop",
				NetworkAttachments: attachments,
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		invClient = &mockInventoryClient{
			getHostLogicalPortMACsFunc: func(_ context.Context, _ string) (map[string]string, error) {
				return map[string]string{"data-0": "aa:bb:cc:dd:ee:01"}, nil
			},
		}
	})

	Describe("reconcileNetworking", func() {
		Context("when no network attachments are configured", func() {
			BeforeEach(func() {
				apiID = "bmi-api-skip"
				bmi = bmiWithAttachments(apiID, nil)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:              k8sClient,
					Scheme:              k8sClient.Scheme(),
					InventoryClient:     invClient,
					NetworkingNamespace: testNetworkingNS,
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should skip with condition set to True/Skipped", func() {
				result, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("Skipped"))
			})
		})

		Context("when NetworkingNamespace is empty", func() {
			BeforeEach(func() {
				apiID = "bmi-api-disabled"
				bmi = bmiWithAttachments(apiID, []v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true, SecurityGroupRefs: []string{"sg-1"}},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:              k8sClient,
					Scheme:              k8sClient.Scheme(),
					InventoryClient:     invClient,
					NetworkingNamespace: "",
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should set ProvisioningDisabled without requiring NetworkingProvider", func() {
				result, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProvisioningDisabled))
			})
		})

		Context("when network attachments are configured with NetworkingNamespace", func() {
			BeforeEach(func() {
				apiID = "bmi-api-" + metav1.Now().Format("150405.000")
				bmi = bmiWithAttachments(apiID, []v1alpha1.BareMetalNetworkAttachment{
					{
						SubnetRef:         "subnet-1",
						Interface:         "data-0",
						Primary:           true,
						SecurityGroupRefs: []string{"sg-1"},
					},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				reconciler = &BareMetalInstanceReconciler{
					Client:                        k8sClient,
					Scheme:                        k8sClient.Scheme(),
					InventoryClient:               invClient,
					NetworkingNamespace:           testNetworkingNS,
					ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
				}
			})

			AfterEach(func() {
				forceDeleteNetworkAttachment(ctx, testNetworkingNS,
					opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID))
				fresh := &v1alpha1.BareMetalInstance{}
				if k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh) == nil {
					fresh.Finalizers = nil
					_ = k8sClient.Update(ctx, fresh)
					_ = k8sClient.Delete(ctx, fresh)
				}
			})

			It("should add the networking finalizer", func() {
				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
			})

			It("should create a hub NetworkAttachment with MAC and mirror Progressing", func() {
				invClient.getHostLogicalPortMACsFunc = func(_ context.Context, _ string) (map[string]string, error) {
					return map[string]string{"data-0": "AA-BB-CC-DD-EE-01"}, nil
				}

				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

				result, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))

				Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].MACAddress).To(Equal("aa:bb:cc:dd:ee:01"))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].Interface).To(Equal("data-0"))

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))

				naName := opv1alpha1.NetworkAttachmentName(
					opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
				na := &opv1alpha1.NetworkAttachment{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNetworkingNS, Name: naName,
				}, na)).To(Succeed())
				Expect(na.Spec.Target.Kind).To(Equal(opv1alpha1.NetworkAttachmentTargetBareMetalInstance))
				Expect(na.Spec.Target.ID).To(Equal(apiID))
				Expect(na.Spec.Subnet).To(Equal("subnet-1"))
				Expect(na.Spec.Interface.Type).To(Equal(opv1alpha1.NetworkInterfacePhysical))
				Expect(na.Spec.Interface.Name).To(Equal("data-0"))
				Expect(na.Spec.Interface.MACAddress).To(Equal("aa:bb:cc:dd:ee:01"))
				Expect(na.Spec.SecurityGroups).To(Equal([]string{"sg-1"}))
				Expect(na.Annotations[BareMetalInstanceTenantAnnotation]).To(Equal("tenant-a"))
				Expect(controllerutil.ContainsFinalizer(na, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
			})

			It("rejects malformed and non-six-byte inventory MACs", func() {
				for _, mac := range []string{
					"aa:bb:cc:dd:ee:gg",
					"aa:bb:cc:dd:ee:ff:00",
					"aabb.ccdd.ee01",
				} {
					bmi.Status.NetworkAttachmentStatuses = nil
					invClient.getHostLogicalPortMACsFunc = func(_ context.Context, _ string) (map[string]string, error) {
						return map[string]string{"data-0": mac}, nil
					}

					Expect(reconciler.ensureNetworkAttachmentStatusMAC(ctx, bmi)).To(HaveOccurred(), mac)
					Expect(bmi.Status.NetworkAttachmentStatuses[0].MACAddress).To(BeEmpty(), mac)
				}
			})

			It("reads NetworkAttachments through the uncached API reader", func() {
				cachedClient := &readTrackingClient{Client: k8sClient}
				apiReader := &readTrackingReader{Reader: k8sClient}
				reconciler.Client = cachedClient
				reconciler.APIReader = apiReader
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
					Interface:  "data-0",
					SubnetRef:  "subnet-1",
					MACAddress: "aa:bb:cc:dd:ee:01",
					Primary:    true,
				}}

				name := opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
				na, err := reconciler.buildBareMetalNetworkAttachment(bmi, apiID, name)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, na)).To(Succeed())

				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(apiReader.getCalls).To(Equal(1))
				Expect(cachedClient.getCalls).To(BeZero())
			})

			It("fails closed when the BMI tenant annotation is missing", func() {
				delete(bmi.Annotations, BareMetalInstanceTenantAnnotation)
				Expect(k8sClient.Update(ctx, bmi)).To(Succeed())

				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred()) // first reconcile adds the finalizer
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentFailed))
				Expect(cond.Message).To(ContainSubstring(BareMetalInstanceTenantAnnotation))
				Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))

				naName := opv1alpha1.NetworkAttachmentName(
					opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
				err = k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNetworkingNS, Name: naName,
				}, &opv1alpha1.NetworkAttachment{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})

			It("should mirror NetworkAttachment Ready=True to Succeeded", func() {
				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				naName := opv1alpha1.NetworkAttachmentName(
					opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
				na := &opv1alpha1.NetworkAttachment{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNetworkingNS, Name: naName,
				}, na)).To(Succeed())
				na.Status.Conditions = []metav1.Condition{{
					Type:               opv1alpha1.NetworkConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             opv1alpha1.ReasonAsExpected,
					Message:            "apply accepted",
					LastTransitionTime: metav1.Now(),
				}}
				Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("Succeeded"))
			})

			It("should mirror NetworkAttachment Failed to NetworkAttachmentFailed", func() {
				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				naName := opv1alpha1.NetworkAttachmentName(
					opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID)
				na := &opv1alpha1.NetworkAttachment{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNetworkingNS, Name: naName,
				}, na)).To(Succeed())
				na.Status.Conditions = []metav1.Condition{{
					Type:               opv1alpha1.NetworkConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             opv1alpha1.ReasonFailed,
					Message:            "fabric apply rejected",
					LastTransitionTime: metav1.Now(),
				}}
				Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentFailed))
				Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			})
		})
	})

	Describe("reconcileNetworkingDeletion", func() {
		Context("when networking finalizer is not present", func() {
			BeforeEach(func() {
				apiID = "bmi-api-del-none"
				bmi = bmiWithAttachments(apiID, nil)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should return done=true immediately", func() {
				_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())
			})
		})

		Context("when networking finalizer is present but NetworkingNamespace is empty", func() {
			BeforeEach(func() {
				apiID = "bmi-api-del-disabled"
				bmi = bmiWithAttachments(apiID, nil)
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:              k8sClient,
					Scheme:              k8sClient.Scheme(),
					NetworkingNamespace: "",
				}
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, bmi)
			})

			It("should remove finalizer and return done=true", func() {
				_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}, fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
			})
		})

		Context("when a NetworkAttachment exists", func() {
			var na *opv1alpha1.NetworkAttachment

			BeforeEach(func() {
				apiID = "bmi-api-del-" + metav1.Now().Format("150405.000")
				bmi = bmiWithAttachments(apiID, []v1alpha1.BareMetalNetworkAttachment{
					{
						SubnetRef:         "subnet-1",
						Interface:         "data-0",
						Primary:           true,
						SecurityGroupRefs: []string{"sg-1"},
					},
				})
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				na = &opv1alpha1.NetworkAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:       opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
						Namespace:  testNetworkingNS,
						Finalizers: []string{BareMetalInstanceNetworkingFinalizer},
					},
					Spec: opv1alpha1.NetworkAttachmentSpec{
						Target: opv1alpha1.NetworkAttachmentTarget{
							Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance,
							ID:   apiID,
						},
						Subnet: "subnet-1",
						Interface: opv1alpha1.NetworkAttachmentInterface{
							Type:       opv1alpha1.NetworkInterfacePhysical,
							Name:       "data-0",
							MACAddress: "aa:bb:cc:dd:ee:01",
						},
						SecurityGroups: []string{"sg-1"},
					},
				}
				Expect(k8sClient.Create(ctx, na)).To(Succeed())

				reconciler = &BareMetalInstanceReconciler{
					Client:                        k8sClient,
					APIReader:                     k8sClient,
					Scheme:                        k8sClient.Scheme(),
					NetworkingNamespace:           testNetworkingNS,
					ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
				}
			})

			AfterEach(func() {
				forceDeleteNetworkAttachment(ctx, testNetworkingNS,
					opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID))
				_ = k8sClient.Delete(ctx, bmi)
			})

			It("should delete the NetworkAttachment, clear its finalizer, and remove the BMI finalizer", func() {
				_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())
				Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
					To(Equal(v1alpha1.HostConditionReasonDetaching))

				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				Eventually(func(g Gomega) {
					_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
					g.Expect(err).NotTo(HaveOccurred())
					if !done {
						_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)
					}
					g.Expect(done).To(BeTrue())
				}).Should(Succeed())

				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(na), &opv1alpha1.NetworkAttachment{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
			})
		})
	})
})

var _ = Describe("BareMetalInstance network/provision ordering", func() {
	var (
		ctx  context.Context
		bmi  *v1alpha1.BareMetalInstance
		prov *mockProvisioningProvider
	)

	BeforeEach(func() {
		ctx = context.Background()
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-order-",
				Namespace:    "default",
				Labels: map[string]string{
					BareMetalInstanceUUIDLabel: "bmi-order-id",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID: "host-order-1",
				Selector: v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"test": "value"},
				},
				HostClass: "openstack",
				// Non-noop template so the provisioning branch is active.
				TemplateID: "bm_provision",
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{
					{
						SubnetRef:         "subnet-1",
						Interface:         "eth9",
						Primary:           true,
						SecurityGroupRefs: []string{"sg-1"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
	})

	AfterEach(func() {
		forceDeleteNetworkAttachment(ctx, testNetworkingNS,
			opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, "bmi-order-id"))
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
	})

	It("triggers provisioning and blocks networking until provision completes", func() {
		provisionTriggered := false
		prov = &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionTriggered = true
				return &provisioning.ProvisionResult{JobID: "prov-1", InitialState: opv1alpha1.JobStatePending}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   "prov-1",
					State:   opv1alpha1.JobStateSucceeded,
					Message: "Provisioning completed",
				}, nil
			},
		}
		reconciler := &BareMetalInstanceReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			InventoryClient: &mockInventoryClient{
				getHostLogicalPortMACsFunc: func(_ context.Context, _ string) (map[string]string, error) {
					return map[string]string{"eth9": "11:22:33:44:55:66"}, nil
				},
			},
			ProvisioningProvider:          prov,
			NetworkingNamespace:           testNetworkingNS,
			ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
		}

		// First pass triggers provisioning, NOT NetworkAttachment create yet.
		_, err := reconciler.reconcileNetworkProvisionAndDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(provisionTriggered).To(BeTrue(), "provisioning should be triggered first")

		naName := opv1alpha1.NetworkAttachmentName(
			opv1alpha1.NetworkAttachmentTargetBareMetalInstance, "bmi-order-id")
		err = k8sClient.Get(ctx, types.NamespacedName{
			Namespace: testNetworkingNS, Name: naName,
		}, &opv1alpha1.NetworkAttachment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(),
			"NetworkAttachment should NOT exist before provisioning completes")
	})
})

var _ = Describe("BareMetalInstance networking CRD contract", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("rejects network attachment spec changes while keeping the persisted attachment unchanged", func() {
		bmi := &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "bmi-contract-",
				Namespace:    "default",
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID: "host-contract-1",
				Selector: v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"test": "value"},
				},
				TemplateID: "bm_provision",
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{
					SubnetRef:         "subnet-a",
					Interface:         "eth9",
					Primary:           true,
					SecurityGroupRefs: []string{"sg-a"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, bmi) })

		mutate := func(mutateFn func(*v1alpha1.BareMetalInstance)) error {
			stored := &v1alpha1.BareMetalInstance{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), stored)).To(Succeed())
			mutateFn(stored)
			return k8sClient.Update(ctx, stored)
		}

		By("rejecting a changed subnet")
		err := mutate(func(object *v1alpha1.BareMetalInstance) {
			object.Spec.NetworkAttachments[0].SubnetRef = "subnet-b"
		})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())

		By("rejecting a changed interface")
		err = mutate(func(object *v1alpha1.BareMetalInstance) {
			object.Spec.NetworkAttachments[0].Interface = "eth8"
		})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())

		By("rejecting a changed primary designation")
		err = mutate(func(object *v1alpha1.BareMetalInstance) {
			object.Spec.NetworkAttachments[0].Primary = false
		})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())

		By("rejecting an added attachment")
		err = mutate(func(object *v1alpha1.BareMetalInstance) {
			object.Spec.NetworkAttachments = append(object.Spec.NetworkAttachments,
				v1alpha1.BareMetalNetworkAttachment{SubnetRef: "subnet-c"})
		})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue())

		By("keeping the persisted attachment unchanged")
		stored := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), stored)).To(Succeed())
		Expect(stored.Spec.NetworkAttachments).To(HaveLen(1))
		Expect(stored.Spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-a"))
		Expect(stored.Spec.NetworkAttachments[0].Interface).To(Equal("eth9"))
		Expect(stored.Spec.NetworkAttachments[0].Primary).To(BeTrue())
		Expect(stored.Spec.NetworkAttachments[0].SecurityGroupRefs).To(Equal([]string{"sg-a"}))

		By("allowing a metadata update")
		err = mutate(func(object *v1alpha1.BareMetalInstance) {
			object.SetLabels(map[string]string{"contract-test": "allowed"})
		})
		Expect(err).ToNot(HaveOccurred())
	})
})
