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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("BareMetalInstance DHCP lease projection", func() {
	var (
		ctx        context.Context
		bmi        *v1alpha1.BareMetalInstance
		reconciler *BareMetalInstanceReconciler
		na         *opv1alpha1.NetworkAttachment
	)

	BeforeEach(func() {
		ctx = context.Background()
		name := fmt.Sprintf("bm-lease-%d", time.Now().UnixNano())
		apiID := "api-" + name
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNetworkingNamespace,
				Labels:    map[string]string{BareMetalInstanceUUIDLabel: apiID},
				Annotations: map[string]string{
					bareMetalInstanceTenantAnnotation: "tenant-a",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				Selector:   v1alpha1.HostSelectorSpec{HostSelector: map[string]string{"test": "networking"}},
				TemplateID: shared.OsacNoopTemplate,
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{
					SubnetRef: "subnet-a", Interface: "data-0", SecurityGroupRefs: []string{"sg-a"}, Primary: true,
				}},
			},
		}
		reconciler = &BareMetalInstanceReconciler{
			Client:                        k8sClient,
			Scheme:                        k8sClient.Scheme(),
			NetworkingEnabled:             true,
			NetworkingNamespace:           testNetworkingNamespace,
			ProvisionPollIntervalDuration: time.Second,
		}
		na = &opv1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
				Namespace:  testNetworkingNamespace,
				Finalizers: []string{BareMetalInstanceNetworkingFinalizer},
				Annotations: map[string]string{
					bareMetalInstanceTenantAnnotation: "tenant-a",
				},
			},
			Spec: opv1alpha1.NetworkAttachmentSpec{
				Target:         opv1alpha1.NetworkAttachmentTarget{Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance, ID: apiID},
				Subnet:         "subnet-a",
				Interface:      opv1alpha1.NetworkAttachmentInterface{Type: opv1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:01"},
				SecurityGroups: []string{"sg-a"},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Succeeded", "handoff completed")
		DeferCleanup(func() {
			latestNA := &opv1alpha1.NetworkAttachment{}
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(na), latestNA); err == nil {
				latestNA.Finalizers = nil
				_ = k8sClient.Update(context.Background(), latestNA)
				_ = k8sClient.Delete(context.Background(), latestNA)
			}
			latestBMI := &v1alpha1.BareMetalInstance{}
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(bmi), latestBMI); err == nil {
				latestBMI.Finalizers = nil
				_ = k8sClient.Update(context.Background(), latestBMI)
				_ = k8sClient.Delete(context.Background(), latestBMI)
			}
		})
	})

	It("waits for a lease while the operator is still querying", func() {
		na.Status.Jobs = []opv1alpha1.NetworkJobStatus{{
			Task: bareMetalQueryDHCPLeaseTask, Manager: opv1alpha1.NetworkJobManagerFabric,
			State: opv1alpha1.NetworkJobStateRetrying, Message: "retrying query", JobID: "job-9",
		}}
		Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		condition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
		Expect(condition.Message).To(ContainSubstring("job-9"))
	})

	It("mirrors an accepted DHCP lease into the existing BMI attachment status", func() {
		na.Status.DHCPLease = &opv1alpha1.NetworkAttachmentDHCPLease{
			IPAddress: "192.0.2.44", MACAddress: "aa:bb:cc:dd:ee:01",
		}
		Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
		Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(Equal("192.0.2.44"))
		condition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
	})

	It("fails discovery when the Fabric query fails even if attachment Ready stays true", func() {
		na.Status.Conditions = []metav1.Condition{{
			Type: opv1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: opv1alpha1.ReasonSucceeded, Message: "attachment applied", LastTransitionTime: metav1.Now(),
		}}
		na.Status.Jobs = []opv1alpha1.NetworkJobStatus{{
			Task: bareMetalQueryDHCPLeaseTask, Manager: opv1alpha1.NetworkJobManagerFabric,
			State: opv1alpha1.NetworkJobStateFailed, Message: "fabric query rejected", JobID: "job-10",
		}}
		Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		condition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
		Expect(condition.Message).To(ContainSubstring("fabric query rejected"))
		Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
	})

	It("skips lease discovery using the explicit networking flag", func() {
		reconciler.NetworkingEnabled = false
		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		condition := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal(opv1alpha1.ReasonProvisioningDisabled))
		Expect(bmi.Status.NetworkAttachmentStatuses).To(BeEmpty())
	})
})
