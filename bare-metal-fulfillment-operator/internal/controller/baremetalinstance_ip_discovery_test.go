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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("BareMetalInstance IP Discovery (NetworkAttachment lease mirror)", func() {
	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
		apiID      string
		recorder   *events.FakeRecorder
	)

	bmiForLeaseMirror := func(attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-lease-",
				Namespace:    "default",
				Labels:       map[string]string{BareMetalInstanceUUIDLabel: apiID},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID: "host-456",
				Selector: v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"test": "value"},
				},
				HostClass:          "metal3",
				TemplateID:         "noop",
				NetworkAttachments: attachments,
			},
		}
	}

	markHandoffComplete := func(instance *v1alpha1.BareMetalInstance) {
		instance.SetStatusCondition(
			v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Succeeded", "Host rebooted onto the tenant network",
		)
	}

	createNAWithLease := func(mac, ip string) {
		na := &opv1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
				Namespace: testNetworkingNS,
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
					MACAddress: mac,
				},
				SecurityGroups: []string{"sg-1"},
			},
		}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		if ip != "" {
			na.Status.DHCPLease = &opv1alpha1.NetworkAttachmentDHCPLease{
				IPAddress:  ip,
				MACAddress: mac,
			}
			Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		apiID = "lease-api-" + metav1.Now().Format("150405.000")
		recorder = &events.FakeRecorder{Events: make(chan string, 8)}
	})

	AfterEach(func() {
		if bmi != nil {
			_ = k8sClient.Delete(ctx, bmi)
		}
		forceDeleteNetworkAttachment(ctx, testNetworkingNS,
			opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID))
	})

	Describe("reconcileIPDiscovery", func() {
		It("keeps the sole attachment primary even when the spec omits it", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{{
				SubnetRef: "subnet-1", Interface: "data-0",
			}})

			reconciler = &BareMetalInstanceReconciler{}
			reconciler.initNetworkAttachmentStatuses(bmi)

			Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
			Expect(bmi.Status.NetworkAttachmentStatuses[0].Primary).To(BeTrue())
		})

		It("skips when no network attachments are configured", func() {
			bmi = bmiForLeaseMirror(nil)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			reconciler = &BareMetalInstanceReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				NetworkingNamespace: testNetworkingNS,
				Recorder:            recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("Skipped"))
		})

		It("sets ProvisioningDisabled when NetworkingNamespace is empty", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			reconciler = &BareMetalInstanceReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				NetworkingNamespace: "",
				Recorder:            recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProvisioningDisabled))
		})

		It("requeues before handoff completes", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			reconciler = &BareMetalInstanceReconciler{
				Client:                        k8sClient,
				Scheme:                        k8sClient.Scheme(),
				NetworkingNamespace:           testNetworkingNS,
				ProvisionPollIntervalDuration: time.Second,
				Recorder:                      recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)).To(BeNil())
		})

		It("mirrors dhcpLease by selected MAC into BMI status", func() {
			mac := "52:54:00:11:22:33"
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				Interface:  "data-0",
				SubnetRef:  "subnet-1",
				Primary:    true,
				MACAddress: mac,
			}}
			markHandoffComplete(bmi)
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

			createNAWithLease(mac, "10.0.1.50")

			reconciler = &BareMetalInstanceReconciler{
				Client:                        k8sClient,
				Scheme:                        k8sClient.Scheme(),
				NetworkingNamespace:           testNetworkingNS,
				ProvisionPollIntervalDuration: time.Second,
				Recorder:                      recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("Succeeded"))
			Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
			Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(Equal("10.0.1.50"))
		})

		It("ignores dhcpLease with a mismatched MAC and stays LeasePending", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				Interface:  "data-0",
				SubnetRef:  "subnet-1",
				Primary:    true,
				MACAddress: "52:54:00:11:22:33",
			}}
			markHandoffComplete(bmi)
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

			createNAWithLease("52:54:00:aa:bb:cc", "10.0.1.50")

			reconciler = &BareMetalInstanceReconciler{
				Client:                        k8sClient,
				Scheme:                        k8sClient.Scheme(),
				NetworkingNamespace:           testNetworkingNS,
				ProvisionPollIntervalDuration: time.Second,
				Recorder:                      recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonLeasePending))
			Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(BeEmpty())
		})

		It("includes the latest query message and AAP job ID in LeasePending", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			markHandoffComplete(bmi)
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

			createNAWithLease("52:54:00:11:22:33", "")
			na := &opv1alpha1.NetworkAttachment{}
			naKey := client.ObjectKey{
				Namespace: testNetworkingNS,
				Name:      opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
			}
			Expect(k8sClient.Get(ctx, naKey, na)).To(Succeed())
			na.Status.Jobs = []opv1alpha1.NetworkJobStatus{
				{Task: networkAttachmentApplyTask, Manager: opv1alpha1.NetworkJobManagerFabric,
					State: opv1alpha1.NetworkJobStateSucceeded, Message: "attachment applied", JobID: "apply-123"},
				{Task: networkAttachmentQueryTask, Manager: opv1alpha1.NetworkJobManagerFabric,
					State: opv1alpha1.NetworkJobStateRunning, Message: "latest lease query", JobID: "query-456"},
			}
			Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

			reconciler = &BareMetalInstanceReconciler{
				Client:                        k8sClient,
				Scheme:                        k8sClient.Scheme(),
				NetworkingNamespace:           testNetworkingNS,
				ProvisionPollIntervalDuration: time.Second,
				Recorder:                      recorder,
			}

			result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))
			condition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonLeasePending))
			Expect(condition.Message).To(Equal("latest lease query (AAP job ID: query-456)"))
		})

		It("emits LeaseNotFound after the warning window only once", func() {
			bmi = bmiForLeaseMirror([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				Interface:  "data-0",
				SubnetRef:  "subnet-1",
				Primary:    true,
				MACAddress: "52:54:00:11:22:33",
			}}
			markHandoffComplete(bmi)
			handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
			handoff.LastTransitionTime = metav1.NewTime(time.Now().Add(-LeaseNotFoundWarningAfter - time.Minute))
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

			createNAWithLease("52:54:00:11:22:33", "")

			reconciler = &BareMetalInstanceReconciler{
				Client:                        k8sClient,
				Scheme:                        k8sClient.Scheme(),
				NetworkingNamespace:           testNetworkingNS,
				ProvisionPollIntervalDuration: time.Second,
				Recorder:                      recorder,
			}

			_, err := reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Eventually(recorder.Events).Should(Receive(ContainSubstring(eventReasonLeaseNotFound)))

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonLeasePending))
			Expect(cond.Message).To(ContainSubstring("No tenant DHCP lease"))

			_, err = reconciler.reconcileIPDiscovery(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Consistently(recorder.Events).ShouldNot(Receive())
		})
	})
})
