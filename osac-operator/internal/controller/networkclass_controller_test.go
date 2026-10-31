/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("NetworkClass resolution", func() {
	var (
		namespace  string
		reconciler *NetworkClassReconciler
	)

	registration := func(name, label string, data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{label: "true"}},
			Immutable:  ptr.To(true),
			Data:       data,
		}
	}
	fabricRegistration := func() *corev1.ConfigMap {
		return registration("fabric", networkmanager.LabelFabricManager, map[string]string{
			"name": "acme_fabric", "ansible_role": "acme.network.fabric", "network_types": "evpn"})
	}
	k8sRegistration := func() *corev1.ConfigMap {
		return registration("k8s", networkmanager.LabelK8sManager, map[string]string{
			"name": "cudn_evpn", "ansible_role": "osac.templates.cudn_evpn", "network_type": "evpn"})
	}
	withICMP := func(cm *corev1.ConfigMap) *corev1.ConfigMap {
		cm.Data["capabilities"] = "security_group_icmp"
		return cm
	}
	newClassWithDefaults := func(k8sManager string, mutate func(*v1alpha1.NetworkClassDefaults)) *v1alpha1.NetworkClass {
		nc := &v1alpha1.NetworkClass{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: namespace,
				Labels: map[string]string{osacNetworkClassIDLabel: "nc-1"}},
			Spec: v1alpha1.NetworkClassSpec{
				FabricManager: "acme_fabric", K8sManager: k8sManager,
				Defaults:    v1alpha1.NetworkClassDefaults{VirtualNetworkIPv4CIDR: "10.0.0.0/16", SubnetIPv4CIDR: "10.0.1.0/24"},
				AddressPlan: v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4},
			},
		}
		if mutate != nil {
			mutate(&nc.Spec.Defaults)
		}
		Expect(k8sClient.Create(ctx, nc)).To(Succeed())
		return nc
	}
	newClass := func(k8sManager string) *v1alpha1.NetworkClass {
		return newClassWithDefaults(k8sManager, nil)
	}
	resolve := func(nc *v1alpha1.NetworkClass) *v1alpha1.NetworkClass {
		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(nc)})
		Expect(err).NotTo(HaveOccurred())
		updated := &v1alpha1.NetworkClass{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nc), updated)).To(Succeed())
		return updated
	}

	BeforeEach(func() {
		namespace = crdTestName("ncres")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
		reconciler = &NetworkClassReconciler{Client: k8sClient, APIReader: k8sClient,
			NetworkingNamespace: namespace, RegistrationNamespace: namespace, ProvisioningEnabled: true}
	})

	It("resolves registrations created before the CR and records the managers and network type", func() {
		Expect(k8sClient.Create(ctx, fabricRegistration())).To(Succeed())
		Expect(k8sClient.Create(ctx, k8sRegistration())).To(Succeed())
		nc := resolve(newClass("cudn_evpn"))
		ready := meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(v1alpha1.ReasonSucceeded))
		Expect(nc.Status.NetworkManagers.Fabric).To(Equal(&v1alpha1.NetworkManagerRef{Name: "acme_fabric", AnsibleRole: "acme.network.fabric"}))
		Expect(nc.Status.NetworkManagers.Kubernetes).To(Equal(&v1alpha1.NetworkManagerRef{Name: "cudn_evpn", AnsibleRole: "osac.templates.cudn_evpn"}))
		Expect(nc.Status.NetworkType).To(Equal(v1alpha1.NetworkTypeEVPN))
	})

	It("leaves the network type empty without a Kubernetes Manager", func() {
		Expect(k8sClient.Create(ctx, fabricRegistration())).To(Succeed())
		nc := resolve(newClass(""))
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(nc.Status.NetworkManagers.Kubernetes).To(BeNil())
		Expect(nc.Status.NetworkType).To(BeEmpty())
	})

	It("marks the NetworkClass ready without resolving managers when provisioning is disabled", func() {
		reconciler.ProvisioningEnabled = false
		nc := resolve(newClass("example_k8s"))

		ready := meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(v1alpha1.ReasonProvisioningDisabled))
		Expect(ready.ObservedGeneration).To(Equal(nc.Generation))
		Expect(nc.Status.NetworkManagers).To(BeNil())
		Expect(nc.Status.NetworkType).To(BeEmpty())
		Expect(nc.Status.SecurityGroupICMP).To(BeNil())
	})

	It("records security_group_icmp true when every manager declares it", func() {
		Expect(k8sClient.Create(ctx, withICMP(fabricRegistration()))).To(Succeed())
		Expect(k8sClient.Create(ctx, withICMP(k8sRegistration()))).To(Succeed())
		nc := resolve(newClass("cudn_evpn"))
		Expect(nc.Status.SecurityGroupICMP).To(Equal(ptr.To(true)))
	})

	It("records false when the Kubernetes Manager does not declare it", func() {
		Expect(k8sClient.Create(ctx, withICMP(fabricRegistration()))).To(Succeed())
		Expect(k8sClient.Create(ctx, k8sRegistration())).To(Succeed())
		nc := resolve(newClass("cudn_evpn"))
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(nc.Status.SecurityGroupICMP).NotTo(BeNil())
		Expect(*nc.Status.SecurityGroupICMP).To(BeFalse())
	})

	It("records true for a Fabric Manager alone that declares it", func() {
		Expect(k8sClient.Create(ctx, withICMP(fabricRegistration()))).To(Succeed())
		nc := resolve(newClass(""))
		Expect(nc.Status.SecurityGroupICMP).To(Equal(ptr.To(true)))
	})

	It("records false when no manager declares it", func() {
		Expect(k8sClient.Create(ctx, fabricRegistration())).To(Succeed())
		nc := resolve(newClass(""))
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(nc.Status.SecurityGroupICMP).NotTo(BeNil())
		Expect(*nc.Status.SecurityGroupICMP).To(BeFalse())
	})

	It("fails a NetworkClass whose default rules use icmp without the capability", func() {
		icmpDefaults := func(d *v1alpha1.NetworkClassDefaults) {
			d.IngressRules = []v1alpha1.SecurityRule{{Protocol: v1alpha1.SecurityGroupProtocolICMP, SourceCIDR: "10.0.0.0/8"}}
		}
		Expect(k8sClient.Create(ctx, fabricRegistration())).To(Succeed())
		nc := resolve(newClassWithDefaults("", icmpDefaults))
		ready := meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(v1alpha1.ReasonFailed))
		Expect(ready.Message).To(Equal("default SecurityGroup rules use protocol icmp but not every " +
			"network manager declares security_group_icmp; use protocol all"))
		Expect(nc.Status.SecurityGroupICMP).To(BeNil())

		// The same defaults resolve once the managers declare the capability.
		Expect(k8sClient.Delete(ctx, fabricRegistration())).To(Succeed())
		Expect(k8sClient.Create(ctx, withICMP(registration("fabric-icmp", networkmanager.LabelFabricManager,
			map[string]string{"name": "acme_fabric", "ansible_role": "acme.network.fabric", "network_types": "evpn"})))).To(Succeed())
		nc = resolve(nc)
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(nc.Status.SecurityGroupICMP).To(Equal(ptr.To(true)))
	})

	It("fails with the reason, resolves again once the registration exists, and never resolves a Ready class again", func() {
		nc := resolve(newClass("cudn_evpn"))
		ready := meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(v1alpha1.ReasonFailed))
		Expect(ready.Message).To(ContainSubstring(`fabric manager "acme_fabric" matches no ConfigMap`))
		Expect(nc.Status.SecurityGroupICMP).To(BeNil())

		Expect(k8sClient.Create(ctx, fabricRegistration())).To(Succeed())
		Expect(reconciler.notReadyNetworkClasses(ctx, nil)).To(HaveLen(1))
		nc = resolve(nc)
		Expect(meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady).Message).
			To(Equal(`kubernetes manager "cudn_evpn" matches no ConfigMap labelled osac.openshift.io/network-k8s-manager`))

		Expect(k8sClient.Create(ctx, k8sRegistration())).To(Succeed())
		nc = resolve(nc)
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(reconciler.notReadyNetworkClasses(ctx, nil)).To(BeEmpty())

		// Deleting a registration of a READY class keeps it READY.
		Expect(k8sClient.Delete(ctx, k8sRegistration())).To(Succeed())
		nc = resolve(nc)
		Expect(meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady)).To(BeTrue())
		Expect(nc.Status.NetworkManagers.Kubernetes).NotTo(BeNil())
	})

	It("fails a registration that is not immutable", func() {
		cm := fabricRegistration()
		cm.Immutable = nil
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())
		nc := resolve(newClass(""))
		Expect(meta.FindStatusCondition(nc.Status.Conditions, v1alpha1.NetworkConditionReady).Message).
			To(Equal("ConfigMap " + namespace + "/fabric must set immutable: true"))
	})
})

var _ = Describe("NetworkClass registration events", func() {
	p := registrationChanged()
	labelled := func(labels map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Labels: labels}}
	}
	fabric := map[string]string{networkmanager.LabelFabricManager: "true"}

	It("reacts to creating or deleting a registration", func() {
		Expect(p.Create(event.TypedCreateEvent[*corev1.ConfigMap]{Object: labelled(fabric)})).To(BeTrue())
		Expect(p.Delete(event.TypedDeleteEvent[*corev1.ConfigMap]{Object: labelled(fabric)})).To(BeTrue())
		Expect(p.Create(event.TypedCreateEvent[*corev1.ConfigMap]{Object: labelled(nil)})).To(BeFalse())
	})

	It("reacts to adding or removing a manager-type label only", func() {
		Expect(p.Update(event.TypedUpdateEvent[*corev1.ConfigMap]{ObjectOld: labelled(nil), ObjectNew: labelled(fabric)})).To(BeTrue())
		Expect(p.Update(event.TypedUpdateEvent[*corev1.ConfigMap]{ObjectOld: labelled(fabric), ObjectNew: labelled(nil)})).To(BeTrue())
		changed := labelled(fabric)
		changed.Data = map[string]string{"description": "x"}
		Expect(p.Update(event.TypedUpdateEvent[*corev1.ConfigMap]{ObjectOld: labelled(fabric), ObjectNew: changed})).To(BeFalse())
	})
})

var _ = Describe("NetworkClass feedback", func() {
	It("maps the resolution to the record", func() {
		nc := &v1alpha1.NetworkClass{}
		remote := privatev1.NetworkClass_builder{Status: &privatev1.NetworkClassStatus{}}.Build()
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))

		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Failed", Message: "no registration"})
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
		Expect(remote.GetStatus().GetMessage()).To(Equal("no registration"))

		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Succeeded"})
		nc.Status.NetworkManagers = &v1alpha1.NetworkManagers{
			Fabric:     &v1alpha1.NetworkManagerRef{Name: "f", AnsibleRole: "a.b.f"},
			Kubernetes: &v1alpha1.NetworkManagerRef{Name: "k", AnsibleRole: "osac.b.k"},
		}
		nc.Status.NetworkType = v1alpha1.NetworkTypeEVPN
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(remote.GetStatus().HasMessage()).To(BeFalse())
		Expect(remote.GetStatus().GetNetworkManagers().GetKubernetes().GetAnsibleRole()).To(Equal("osac.b.k"))
		Expect(remote.GetStatus().GetNetworkType()).To(Equal("evpn"))
		Expect(remote.GetStatus().HasSecurityGroupIcmp()).To(BeFalse())

		nc.Status.SecurityGroupICMP = ptr.To(true)
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().GetSecurityGroupIcmp()).To(BeTrue())

		nc.Status.SecurityGroupICMP = ptr.To(false)
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().HasSecurityGroupIcmp()).To(BeTrue())
		Expect(remote.GetStatus().GetSecurityGroupIcmp()).To(BeFalse())

		nc.Status.SecurityGroupICMP = nil
		Expect(syncNetworkClass(ctx, nc, remote)).To(Succeed())
		Expect(remote.GetStatus().HasSecurityGroupIcmp()).To(BeFalse())
	})
})

var _ = Describe("Networking feedback state (provisional)", func() {
	It("maps the Ready condition", func() {
		vn := &v1alpha1.VirtualNetwork{}
		state, _ := networkFeedback(vn, vn.Status.Conditions)
		Expect(state).To(Equal(networkFeedbackPending))
		meta.SetStatusCondition(&vn.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonProvisioningDisabled, Message: "m"})
		state, message := networkFeedback(vn, vn.Status.Conditions)
		Expect(state).To(Equal(networkFeedbackReady))
		Expect(message).To(Equal("m"))
		meta.SetStatusCondition(&vn.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: v1alpha1.ReasonFailed})
		state, _ = networkFeedback(vn, vn.Status.Conditions)
		Expect(state).To(Equal(networkFeedbackFailed))
		now := metav1.Now()
		vn.DeletionTimestamp = &now
		state, _ = networkFeedback(vn, vn.Status.Conditions)
		Expect(state).To(Equal(networkFeedbackDeleting))
	})
})
