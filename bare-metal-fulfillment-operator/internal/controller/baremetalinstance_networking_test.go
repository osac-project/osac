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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("BareMetalInstance Networking", func() {
	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
	)

	bmiWithAttachments := func(attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-net-",
				Namespace:    "default",
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
	})

	Describe("reconcileNetworking", func() {
		Context("when no network attachments are configured", func() {
			BeforeEach(func() {
				bmi = bmiWithAttachments(nil)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:             k8sClient,
					Scheme:             k8sClient.Scheme(),
					NetworkingProvider: &mockProvisioningProvider{},
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

		Context("when NetworkingProvider is nil", func() {
			BeforeEach(func() {
				bmi = bmiWithAttachments([]v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:             k8sClient,
					Scheme:             k8sClient.Scheme(),
					NetworkingProvider: nil,
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

		Context("when network attachments are configured with a provider", func() {
			var mockProvider *mockProvisioningProvider

			BeforeEach(func() {
				bmi = bmiWithAttachments([]v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				mockProvider = &mockProvisioningProvider{}
				reconciler = &BareMetalInstanceReconciler{
					Client:                        k8sClient,
					Scheme:                        k8sClient.Scheme(),
					NetworkingProvider:            mockProvider,
					ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should add the networking finalizer", func() {
				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
			})

			It("should call the provisioning provider", func() {
				provisionCalled := false
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					provisionCalled = true
					return &provisioning.ProvisionResult{
						JobID:        "job-1",
						InitialState: opv1alpha1.JobStatePending,
						Message:      "triggered",
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "job-1",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				// First call adds finalizer
				_, err := reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())

				// Refetch after finalizer was added
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

				// Second call triggers provisioning
				_, err = reconciler.reconcileNetworking(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(provisionCalled).To(BeTrue())
			})

			It("should set NetworkAttachmentsReady to True on success", func() {
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "job-1",
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "job-1",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				// Reconcile until complete (finalizer add → trigger → poll+callback)
				// The OnSuccess callback sets the condition in-memory; the outer
				// Reconcile loop would persist it. We check in-memory after each call.
				var foundCond *metav1.Condition
				for range 10 {
					_, _ = reconciler.reconcileNetworking(ctx, bmi)
					foundCond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
					if foundCond != nil && foundCond.Status == metav1.ConditionTrue {
						break
					}
					// Persist status so the next call sees the jobs
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				Expect(foundCond).NotTo(BeNil())
				Expect(foundCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(foundCond.Reason).To(Equal("Succeeded"))
			})

			It("should set NetworkAttachmentsReady to False on failure", func() {
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "job-1",
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID:   "job-1",
						State:   opv1alpha1.JobStateFailed,
						Message: "network configuration failed",
					}, nil
				}

				var foundCond *metav1.Condition
				for range 10 {
					_, _ = reconciler.reconcileNetworking(ctx, bmi)
					foundCond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
					if foundCond != nil && foundCond.Reason == v1alpha1.HostConditionReasonTemplateFailed {
						break
					}
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				Expect(foundCond).NotTo(BeNil())
				Expect(foundCond.Status).To(Equal(metav1.ConditionFalse))
				Expect(foundCond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
			})

			It("should track networking jobs in status", func() {
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "net-job-1",
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "net-job-1",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				// Reconcile multiple times to trigger and poll
				for range 5 {
					_, _ = reconciler.reconcileNetworking(ctx, bmi)
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				Expect(bmi.Status.NetworkingJobs).NotTo(BeEmpty())
				Expect(bmi.Status.NetworkingJobs[0].JobID).To(Equal("net-job-1"))
			})
		})
	})

	Describe("reconcileNetworkingDeletion", func() {
		Context("when networking finalizer is not present", func() {
			BeforeEach(func() {
				bmi = bmiWithAttachments(nil)
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

		Context("when networking finalizer is present but provider is nil", func() {
			BeforeEach(func() {
				bmi = bmiWithAttachments(nil)
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:             k8sClient,
					Scheme:             k8sClient.Scheme(),
					NetworkingProvider: nil,
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

		Context("when networking finalizer is present with provider", func() {
			var mockProvider *mockProvisioningProvider

			BeforeEach(func() {
				bmi = bmiWithAttachments([]v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
				})
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				mockProvider = &mockProvisioningProvider{}
				reconciler = &BareMetalInstanceReconciler{
					Client:                        k8sClient,
					Scheme:                        k8sClient.Scheme(),
					NetworkingProvider:            mockProvider,
					ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
				}
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, bmi)
			})

			It("should remove finalizer after deprovisioning completes", func() {
				mockProvider.triggerDeprovisionFunc = func(_ context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionTriggered,
						JobID:  "deprov-1",
					}, nil
				}
				mockProvider.getDeprovisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "deprov-1",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				var done bool
				for range 10 {
					var err error
					_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
					Expect(err).NotTo(HaveOccurred())
					if done {
						break
					}
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}
				Expect(done).To(BeTrue())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}, fresh)).To(Succeed())
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
		net  *mockProvisioningProvider
	)

	BeforeEach(func() {
		ctx = context.Background()
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-order-",
				Namespace:    "default",
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
					{SubnetRef: "subnet-1", Interface: "eth9", Primary: true},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

		net = &mockProvisioningProvider{}
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
	})

	It("triggers provisioning and blocks networking until provision completes", func() {
		var calls []string
		provisionComplete := false
		networkingStartedAfterProvision := false
		prov = &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				calls = append(calls, "provision")
				return &provisioning.ProvisionResult{JobID: "prov-1", InitialState: opv1alpha1.JobStatePending}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
				state := opv1alpha1.JobStatePending
				if provisionComplete {
					state = opv1alpha1.JobStateSucceeded
				}
				return provisioning.ProvisionStatus{
					JobID:   "prov-1",
					State:   state,
					Message: "Provisioning status",
				}, nil
			},
		}
		net = &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, obj client.Object) (*provisioning.ProvisionResult, error) {
				calls = append(calls, "network")
				instance := obj.(*v1alpha1.BareMetalInstance)
				networkingStartedAfterProvision = instance.IsStatusConditionTrue(v1alpha1.HostConditionProvisionTemplateComplete)
				return &provisioning.ProvisionResult{JobID: "net-1", InitialState: opv1alpha1.JobStatePending}, nil
			},
		}
		reconciler := &BareMetalInstanceReconciler{
			Client:                        k8sClient,
			Scheme:                        k8sClient.Scheme(),
			NetworkingProvider:            net,
			ProvisioningProvider:          prov,
			ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
		}

		// Provisioning may be polled repeatedly while it is pending; the network
		// provider must not be called until the provisioning job succeeds.
		for range 2 {
			_, err := reconciler.reconcileNetworkProvisionAndDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(ConsistOf("provision"))
			Expect(reconciler.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bmi), bmi.Status)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		}
		Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionProvisionTemplateComplete)).To(BeFalse())

		provisionComplete = true
		for range 5 {
			_, err := reconciler.reconcileNetworkProvisionAndDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			if len(calls) == 2 {
				break
			}
			Expect(reconciler.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bmi), bmi.Status)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		}
		Expect(calls).To(Equal([]string{"provision", "network"}))
		Expect(networkingStartedAfterProvision).To(BeTrue(),
			"the tenant network move must start only after the provisioning template succeeds")
	})

	It("completes provisioning, tenant network handoff, reboot, then IP discovery", func() {
		var calls []string
		prov = &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				calls = append(calls, "provision")
				return &provisioning.ProvisionResult{JobID: "prov-flow", InitialState: opv1alpha1.JobStatePending}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: "prov-flow", State: opv1alpha1.JobStateSucceeded}, nil
			},
		}
		net = &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, obj client.Object) (*provisioning.ProvisionResult, error) {
				instance := obj.(*v1alpha1.BareMetalInstance)
				Expect(instance.IsStatusConditionTrue(v1alpha1.HostConditionProvisionTemplateComplete)).To(BeTrue())
				calls = append(calls, "tenant-network-move")
				return &provisioning.ProvisionResult{JobID: "network-flow", InitialState: opv1alpha1.JobStatePending}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: "network-flow", State: opv1alpha1.JobStateSucceeded}, nil
			},
		}
		ipDiscovery := &mockProvisioningProvider{
			triggerProvisionFunc: func(_ context.Context, obj client.Object) (*provisioning.ProvisionResult, error) {
				instance := obj.(*v1alpha1.BareMetalInstance)
				Expect(instance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkAttachmentsReady)).To(BeTrue())
				Expect(instance.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete)).To(BeTrue())
				calls = append(calls, "tenant-network-ip-discovery")
				return &provisioning.ProvisionResult{JobID: "ip-discovery-flow", InitialState: opv1alpha1.JobStatePending}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: "ip-discovery-flow", State: opv1alpha1.JobStateSucceeded}, nil
			},
		}
		managementClient := &mockManagementClient{
			getPowerStateFunc: func(_ context.Context, _ string) (*management.PowerStatus, error) {
				return &management.PowerStatus{State: management.PowerOn}, nil
			},
			triggerRestartFunc: func(_ context.Context, _ string) error {
				calls = append(calls, "handoff-reboot")
				return nil
			},
			isRestartCompleteFunc: func(_ context.Context, _ string) (bool, error) {
				calls = append(calls, "handoff-reboot-complete")
				return true, nil
			},
		}
		reconciler := &BareMetalInstanceReconciler{
			Client:                            k8sClient,
			Scheme:                            k8sClient.Scheme(),
			NetworkingProvider:                net,
			ProvisioningProvider:              prov,
			IPDiscoveryProvider:               ipDiscovery,
			ManagementClient:                  managementClient,
			ProvisionPollIntervalDuration:     DefaultProvisionPollIntervalDuration,
			ManagementRecheckIntervalDuration: DefaultManagementRecheckIntervalDuration,
		}

		for range 12 {
			_, err := reconciler.reconcileNetworkProvisionAndDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			ipCondition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			if ipCondition != nil && ipCondition.Status == metav1.ConditionTrue {
				break
			}
			Expect(reconciler.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bmi), bmi.Status)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		}

		Expect(calls).To(Equal([]string{
			"provision",
			"tenant-network-move",
			"handoff-reboot",
			"handoff-reboot-complete",
			"tenant-network-ip-discovery",
		}))
		Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionProvisionTemplateComplete)).To(BeTrue())
		Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionNetworkAttachmentsReady)).To(BeTrue())
		Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionNetworkHandoffComplete)).To(BeTrue())
		Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionIPDiscoveryComplete)).To(BeTrue())
	})
})
