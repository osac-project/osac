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
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ComputeInstance controller specs of the VMaaS networking design: the
// NetworkAttachment before the VM, the ocp_virt_vm jobs in status.jobs,
// NetworkReady, address discovery, the delete order and the disabled fallback.
var _ = Describe("ComputeInstance VMaaS networking", func() {
	const (
		ns         = "default"
		subnetCIDR = "10.9.0.0/24"
	)
	ctx := context.Background()

	type ciFixture struct {
		r      *ComputeInstanceReconciler
		aap    *wlFakeAAP
		key    types.NamespacedName
		id     string
		tenant string
	}

	request := func(key types.NamespacedName) mcreconcile.Request {
		return mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}}
	}

	// reconcileUntil reconciles until check passes; a reconcile may report a
	// write conflict while the cache catches up.
	reconcileUntil := func(f *ciFixture, check func(g Gomega, ci *osacv1alpha1.ComputeInstance)) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			_, _ = f.r.Reconcile(ctx, request(f.key))
			ci := &osacv1alpha1.ComputeInstance{}
			g.Expect(k8sClient.Get(ctx, f.key, ci)).To(Succeed())
			check(g, ci)
		}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
	}

	condition := func(ci *osacv1alpha1.ComputeInstance, t osacv1alpha1.ComputeInstanceConditionType) *metav1.Condition {
		return ci.GetStatusCondition(t)
	}

	newFixture := func(name string, enabled bool) *ciFixture {
		GinkgoHelper()
		id := name + "-id"
		tenant := name + "-tenant"
		createReadyTenant(ctx, ns, tenant)
		DeferCleanup(func() { deleteTenantInNamespace(ctx, ns, tenant) })
		spec := newTestComputeInstanceSpec("test_template")
		spec.NetworkAttachments = []osacv1alpha1.ComputeNetworkAttachment{{
			SubnetRef:         name + "-subnet",
			SecurityGroupRefs: []string{"sg-a", "sg-b"},
		}}
		ci := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   ns,
				Labels:      map[string]string{osacComputeInstanceIDLabel: id},
				Annotations: map[string]string{osacTenantKey: tenant},
			},
			Spec: spec,
		}
		Expect(k8sClient.Create(ctx, ci)).To(Succeed())
		key := client.ObjectKeyFromObject(ci)
		DeferCleanup(func() {
			current := &osacv1alpha1.ComputeInstance{}
			if k8sClient.Get(ctx, key, current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, current)
				_ = k8sClient.Delete(ctx, current)
			}
			na := &osacv1alpha1.NetworkAttachment{}
			naKey := types.NamespacedName{Namespace: ns, Name: osacv1alpha1.NetworkAttachmentName(osacv1alpha1.NetworkAttachmentTargetComputeInstance, id)}
			if k8sClient.Get(ctx, naKey, na) == nil {
				na.Finalizers = nil
				_ = k8sClient.Update(ctx, na)
				_ = k8sClient.Delete(ctx, na)
			}
		})
		r, fake := newTestCIReconcilerWithAAP(enabled, ns, ns, ns, 100*time.Millisecond)
		Eventually(func() error { return r.Get(ctx, key, &osacv1alpha1.ComputeInstance{}) }, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		return &ciFixture{r: r, aap: fake, key: key, id: id, tenant: tenant}
	}

	networkAttachmentOf := func(f *ciFixture) *osacv1alpha1.NetworkAttachment {
		GinkgoHelper()
		na := &osacv1alpha1.NetworkAttachment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns,
			Name: osacv1alpha1.NetworkAttachmentName(osacv1alpha1.NetworkAttachmentTargetComputeInstance, f.id)}, na)).To(Succeed())
		return na
	}

	setAttachmentStatus := func(f *ciFixture, mutate func(na *osacv1alpha1.NetworkAttachment)) {
		GinkgoHelper()
		Eventually(func() error {
			na := networkAttachmentOf(f)
			mutate(na)
			return k8sClient.Status().Update(ctx, na)
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
	}

	attached := func(namespace string, ready metav1.ConditionStatus, reason, message string) func(*osacv1alpha1.NetworkAttachment) {
		return func(na *osacv1alpha1.NetworkAttachment) {
			if namespace != "" {
				na.Status.VMAttachment = &osacv1alpha1.NetworkAttachmentVMAttachment{Namespace: namespace}
			}
			meta.SetStatusCondition(&na.Status.Conditions, metav1.Condition{
				Type: osacv1alpha1.NetworkConditionReady, Status: ready, Reason: reason, Message: message,
			})
		}
	}

	createNamespace := func(name string) {
		GinkgoHelper()
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, namespace))).To(Succeed())
	}

	createSubnet := func(f *ciFixture, overlay []string) {
		GinkgoHelper()
		subnet := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      f.key.Name + "-subnet",
				Namespace: ns,
				Labels:    map[string]string{osacSubnetIDLabel: f.key.Name + "-subnet"},
			},
			Spec: osacv1alpha1.SubnetSpec{
				VirtualNetwork: "vn-" + f.id,
				IPv4CIDR:       subnetCIDR,
				AddressPlan:    osacv1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4},
			},
		}
		Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, subnet) })
		subnet.Status.IPAM = &osacv1alpha1.SubnetIPAM{GatewayIPv4: "10.9.0.1", OverlayIPv4CIDRs: overlay}
		Expect(k8sClient.Status().Update(ctx, subnet)).To(Succeed())
	}

	createVM := func(f *ciFixture, namespace string, vmiPhase kubevirtv1.VirtualMachineInstancePhase, interfaces ...kubevirtv1.VirtualMachineInstanceNetworkInterface) {
		GinkgoHelper()
		labels := map[string]string{osacComputeInstanceNameLabel: f.key.Name}
		vm := &kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: f.key.Name, Namespace: namespace, Labels: labels},
			Status: kubevirtv1.VirtualMachineStatus{
				PrintableStatus: kubevirtv1.VirtualMachineStatusRunning,
				Conditions:      []kubevirtv1.VirtualMachineCondition{{Type: kubevirtv1.VirtualMachineReady, Status: corev1.ConditionTrue}},
			},
		}
		Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, vm) })
		vmi := &kubevirtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: f.key.Name, Namespace: namespace, Labels: labels},
			Status: kubevirtv1.VirtualMachineInstanceStatus{
				Phase:      vmiPhase,
				Interfaces: interfaces,
				PhaseTransitionTimestamps: []kubevirtv1.VirtualMachineInstancePhaseTransitionTimestamp{{
					Phase: kubevirtv1.Running, PhaseTransitionTimestamp: metav1.Now(),
				}},
			},
		}
		Expect(k8sClient.Create(ctx, vmi)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, vmi) })
	}

	It("creates the NetworkAttachment before the VM and waits for its vm_attachment", func() {
		f := newFixture("vmaas-na", true)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonNetworkAttachmentProgressing))
		})
		na := networkAttachmentOf(f)
		Expect(na.Spec.Target).To(Equal(osacv1alpha1.NetworkAttachmentTarget{Kind: osacv1alpha1.NetworkAttachmentTargetComputeInstance, ID: f.id}))
		Expect(na.Spec.Subnet).To(Equal("vmaas-na-subnet"))
		Expect(na.Spec.Interface).To(Equal(osacv1alpha1.NetworkAttachmentInterface{Type: osacv1alpha1.NetworkInterfaceOverlay}))
		Expect(na.Spec.SecurityGroups).To(Equal([]string{"sg-a", "sg-b"}))
		Expect(na.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
		Expect(na.Annotations).To(HaveKeyWithValue(osacTenantKey, f.tenant))

		setAttachmentStatus(f, attached("", metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, "apply_network_attachment (kubernetes) running in AAP job 7"))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Message).To(
				Equal("apply_network_attachment (kubernetes) running in AAP job 7"))
		})
		Expect(f.aap.tasks()).To(BeEmpty(), "no VM before the Kubernetes apply succeeded")
	})

	It("gates the provision job on tenant storage after the NetworkAttachment is usable", func() {
		f := newFixture("vmaas-storage-gate", true)
		setTenantStorage := func(status metav1.ConditionStatus, classes []osacv1alpha1.ResolvedStorageClass) {
			GinkgoHelper()
			tenant := &osacv1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: f.tenant, Namespace: ns}, tenant)).To(Succeed())
			tenant.SetStatusCondition(osacv1alpha1.TenantConditionStorageReady, status, osacv1alpha1.TenantReasonFound, "storage status of the spec")
			tenant.Status.StorageClasses = classes
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.Tenant{}
				g.Expect(f.r.Get(ctx, types.NamespacedName{Name: f.tenant, Namespace: ns}, cached)).To(Succeed())
				storageReady := cached.GetStatusCondition(osacv1alpha1.TenantConditionStorageReady)
				g.Expect(storageReady).NotTo(BeNil())
				g.Expect(storageReady.Status).To(Equal(status))
				g.Expect(cached.Status.StorageClasses).To(HaveLen(len(classes)))
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		}
		setTenantStorage(metav1.ConditionFalse, nil)
		tiers := &mockStorageTiersLister{
			listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
				return privatev1.StorageTiersListResponse_builder{
					Items: []*privatev1.StorageTier{newTestStorageTier("standard", "be-netapp")},
				}.Build(), nil
			},
		}
		f.r.TiersClient = tiers
		f.r.BackendsClient = &mockStorageBackendsClient{
			getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
				return newTestCSPManagedBackendGetResponse("netapp"), nil
			},
		}

		By("no namespace yet: the attachment step waits and storage is not looked at")
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Reason).To(
				Equal(osacv1alpha1.ComputeInstanceReasonNetworkAttachmentProgressing))
		})
		Expect(tiers.listCallCount).To(BeZero())
		Expect(f.aap.tasks()).To(BeEmpty())

		By("the namespace is known: the gate holds the job while status keeps being reported")
		createNamespace("vmaas-storage-gate-ns")
		setAttachmentStatus(f, attached("vmaas-storage-gate-ns", metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			provisioned := condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned)
			g.Expect(provisioned.Reason).To(Equal(osacv1alpha1.ReasonTenantNotReady))
			g.Expect(provisioned.Message).To(ContainSubstring("StorageReady=False"))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Reason).To(
				Equal(osacv1alpha1.ComputeInstanceReasonVMNotRunning))
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseStarting))
			g.Expect(ci.Status.VirtualMachineReference).To(BeNil())
		})
		result, err := f.r.Reconcile(ctx, request(f.key))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		Expect(f.aap.tasks()).To(BeEmpty())

		By("storage is ready: the job runs in the namespace of the attachment")
		setTenantStorage(metav1.ConditionTrue, []osacv1alpha1.ResolvedStorageClass{{Name: "standard-sc", Tier: "standard"}})
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.VirtualMachineReference).NotTo(BeNil())
			g.Expect(ci.Status.VirtualMachineReference.Namespace).To(Equal("vmaas-storage-gate-ns"))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned).Reason).To(Equal(osacv1alpha1.ReasonWaitingForVM))
		})
		Expect(f.aap.tasks()).To(Equal([]string{computeInstanceProvisionTask}))
		Expect(launchedTenantStorageClasses(f.aap)).To(Equal([]osacv1alpha1.ResolvedStorageClass{{Name: "standard-sc", Tier: "standard"}}))
		Expect(f.aap.lastVars()["vm_network"]).To(HaveKeyWithValue("namespace", "vmaas-storage-gate-ns"))
	})

	It("creates no VM after a failed Kubernetes apply", func() {
		f := newFixture("vmaas-na-failed", true)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached("", metav1.ConditionFalse, osacv1alpha1.ReasonFailed, "only the owner Subnet serves VMs"))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonNetworkAttachmentFailed))
			g.Expect(c.Message).To(Equal("only the owner Subnet serves VMs"))
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
		})
		Expect(f.aap.tasks()).To(BeEmpty())
	})

	It("provisions in the vm_attachment namespace, discovers the address and reports NetworkReady", func() {
		f := newFixture("vmaas-flow", true)
		vmNamespace := "vmaas-flow-net"
		createNamespace(vmNamespace)
		createSubnet(f, []string{"10.9.0.0/25"})
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, "waiting for the VM address"))

		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
			g.Expect(ci.Status.Jobs[0].Task).To(Equal(computeInstanceProvisionTask))
			g.Expect(ci.Status.Jobs[0].Manager).To(Equal(osacv1alpha1.NetworkJobManagerNone))
			g.Expect(ci.Status.Jobs[0].JobID).NotTo(BeEmpty())
			g.Expect(ci.Status.VirtualMachineReference).To(Equal(&osacv1alpha1.VirtualMachineReferenceType{
				Namespace: vmNamespace, KubeVirtVirtualMachineName: f.key.Name,
			}))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Reason).To(
				Equal(osacv1alpha1.ComputeInstanceReasonVMNotRunning))
		})
		launch := f.aap.last(computeInstanceProvisionTask)
		Expect(launch.template).To(Equal("osac-create-compute-instance"))
		Expect(launch.label).To(Equal("osac-provision-none-" + f.id + "-1"))
		Expect(launch.vars["vm_network"]).To(Equal(map[string]any{
			"namespace":  vmNamespace,
			"networks":   []any{map[string]any{"name": "tenant", "pod": map[string]any{}}},
			"interfaces": []any{map[string]any{"name": "tenant", "binding": map[string]any{"name": "l2bridge"}}},
			"labels":     map[string]any{computeInstanceIDVMLabel: f.id},
		}))
		Expect(launch.vars).To(HaveKey("resource"))
		Expect(launch.vars).NotTo(HaveKey("manager"))

		f.aap.finish(computeInstanceProvisionTask, "successful", "")
		createVM(f, vmNamespace, kubevirtv1.Running,
			kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "guest-agent-only", IPs: []string{"10.9.0.40"}},
			kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "tenant", IPs: []string{"fd00::5", "10.9.0.200", "10.9.0.20"}},
		)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.IPAddress).To(Equal("10.9.0.20"))
			g.Expect(ci.Status.ComputeNetworkAttachmentStatuses).To(Equal([]osacv1alpha1.ComputeNetworkAttachmentStatus{{
				SubnetRef: "vmaas-flow-subnet", IPAddress: "10.9.0.20",
			}}))
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonWaitingForFabricBinding))
			g.Expect(c.Message).To(Equal("waiting for the VM address"))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionFalse))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionConfigurationApplied).Status).To(Equal(metav1.ConditionTrue))
		})

		setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionTrue, osacv1alpha1.ReasonSucceeded, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonIPDiscovered))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseRunning))
		})

		By("running the provision job again as the next attempt after a restart request")
		Eventually(func() error {
			ci := &osacv1alpha1.ComputeInstance{}
			if err := k8sClient.Get(ctx, f.key, ci); err != nil {
				return err
			}
			now := metav1.NewTime(time.Now().Truncate(time.Second))
			ci.Spec.RestartRequestedAt = &now
			return k8sClient.Update(ctx, ci)
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionConfigurationApplied).Status).To(Equal(metav1.ConditionFalse))
		})
		Expect(f.aap.last(computeInstanceProvisionTask).label).To(Equal("osac-provision-none-" + f.id + "-2"))
	})

	It("waits for an address in the overlay range", func() {
		f := newFixture("vmaas-noip", true)
		vmNamespace := "vmaas-noip-net"
		createNamespace(vmNamespace)
		createSubnet(f, []string{"10.9.0.0/25"})
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
		})
		f.aap.finish(computeInstanceProvisionTask, "successful", "")
		createVM(f, vmNamespace, kubevirtv1.Running,
			kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "tenant", IPs: []string{"10.9.0.200"}})
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonWaitingForIPAddress))
			g.Expect(c.Message).To(ContainSubstring("10.9.0.0/25"))
			g.Expect(ci.Status.IPAddress).To(BeEmpty())
			g.Expect(ci.Status.ComputeNetworkAttachmentStatuses).To(BeEmpty())
		})
	})

	It("fails the ComputeInstance finally when the provision job fails", func() {
		f := newFixture("vmaas-provfail", true)
		createNamespace("vmaas-provfail-net")
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached("vmaas-provfail-net", metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
		})
		f.aap.finish(computeInstanceProvisionTask, "failed", `{"osac_error": {"message": "template not found", "retryable": false}}`)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned)
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			g.Expect(c.Message).To(Equal("template not found"))
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
		})
		Expect(f.aap.tasks()).To(HaveLen(1))
	})

	Describe("a guest address with no known Subnet range", func() {
		// runningVMWith drives a VM whose tenant interface reports address and
		// whose Subnet is prepared by subnet (nil: no Subnet exists).
		runningVMWith := func(name, address string, subnet func(f *ciFixture)) *ciFixture {
			GinkgoHelper()
			f := newFixture(name, true)
			vmNamespace := name + "-net"
			createNamespace(vmNamespace)
			if subnet != nil {
				subnet(f)
			}
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
			})
			setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs).To(HaveLen(1))
			})
			f.aap.finish(computeInstanceProvisionTask, "successful", "")
			createVM(f, vmNamespace, kubevirtv1.Running,
				kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "tenant", IPs: []string{address}})
			return f
		}

		expectNoAddress := func(f *ciFixture) {
			GinkgoHelper()
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
				g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(c.Reason).To(Equal(osacv1alpha1.ComputeInstanceReasonWaitingForIPAddress))
				g.Expect(c.Message).To(ContainSubstring("address range of the Subnet"))
			})
			Consistently(func(g Gomega) {
				res, _ := f.r.Reconcile(ctx, request(f.key))
				ci := mustGetCI(f.key)
				g.Expect(ci.Status.IPAddress).To(BeEmpty())
				g.Expect(ci.Status.ComputeNetworkAttachmentStatuses).To(BeEmpty())
				g.Expect(res.RequeueAfter).NotTo(BeZero(), "polls until the range arrives")
			}, 500*time.Millisecond, 100*time.Millisecond).Should(Succeed())
		}

		It("publishes no address when the Subnet has no status.ipam", func() {
			f := runningVMWith("vmaas-noipam", "10.9.0.20", func(f *ciFixture) {
				subnet := &osacv1alpha1.Subnet{
					ObjectMeta: metav1.ObjectMeta{
						Name: f.key.Name + "-subnet", Namespace: ns,
						Labels: map[string]string{osacSubnetIDLabel: f.key.Name + "-subnet"},
					},
					Spec: osacv1alpha1.SubnetSpec{
						VirtualNetwork: "vn-" + f.id, IPv4CIDR: subnetCIDR,
						AddressPlan: osacv1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4},
					},
				}
				Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
				DeferCleanup(func() { _ = k8sClient.Delete(ctx, subnet) })
			})
			expectNoAddress(f)
		})

		It("publishes no address when the Subnet is missing", func() {
			expectNoAddress(runningVMWith("vmaas-nosubnet", "10.9.0.20", nil))
		})

		It("keeps the validated address of a Ready VM when the Subnet loses its range", func() {
			f := runningVMWith("vmaas-blip", "10.9.0.20", func(f *ciFixture) { createSubnet(f, []string{"10.9.0.0/25"}) })
			setAttachmentStatus(f, attached("vmaas-blip-net", metav1.ConditionTrue, osacv1alpha1.ReasonSucceeded, ""))
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.IPAddress).To(Equal("10.9.0.20"))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
			})

			subnetKey := types.NamespacedName{Namespace: ns, Name: f.key.Name + "-subnet"}
			Eventually(func() error {
				subnet := &osacv1alpha1.Subnet{}
				if err := k8sClient.Get(ctx, subnetKey, subnet); err != nil {
					return err
				}
				subnet.Status.IPAM = nil
				return k8sClient.Status().Update(ctx, subnet)
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
			Eventually(func(g Gomega) {
				subnet := &osacv1alpha1.Subnet{}
				g.Expect(k8sClient.Get(ctx, subnetKey, subnet)).To(Succeed())
				g.Expect(subnet.Status.IPAM).To(BeNil())
			}).Should(Succeed())

			Consistently(func(g Gomega) {
				res, _ := f.r.Reconcile(ctx, request(f.key))
				ci := mustGetCI(f.key)
				g.Expect(ci.Status.IPAddress).To(Equal("10.9.0.20"))
				g.Expect(ci.Status.ComputeNetworkAttachmentStatuses).To(HaveLen(1))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Status).To(Equal(metav1.ConditionTrue))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
				g.Expect(res.RequeueAfter).NotTo(BeZero(), "polls until the range returns")
			}, 1500*time.Millisecond, 200*time.Millisecond).Should(Succeed())
		})

		It("warns once the VM has had no usable address for ten minutes while the range is unknown", func() {
			f := runningVMWith("vmaas-warn", "10.9.0.20", nil)
			recorder := events.NewFakeRecorder(100)
			f.r.Recorder = recorder
			f.r.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
			Eventually(func(g Gomega) {
				_, _ = f.r.Reconcile(ctx, request(f.key))
				g.Expect(recorder.Events).To(Receive(ContainSubstring(eventReasonIPAddressNotFound)))
			}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("publishes no address outside the range and the address once inside it", func() {
			f := runningVMWith("vmaas-range", "10.9.0.200", func(f *ciFixture) { createSubnet(f, []string{"10.9.0.0/25"}) })
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady).Message).To(ContainSubstring("10.9.0.0/25"))
				g.Expect(ci.Status.IPAddress).To(BeEmpty())
			})

			Eventually(func() error {
				vmi := &kubevirtv1.VirtualMachineInstance{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "vmaas-range-net", Name: f.key.Name}, vmi); err != nil {
					return err
				}
				vmi.Status.Interfaces = []kubevirtv1.VirtualMachineInstanceNetworkInterface{{Name: "tenant", IPs: []string{"10.9.0.20"}}}
				return k8sClient.Update(ctx, vmi)
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.IPAddress).To(Equal("10.9.0.20"))
			})
		})
	})

	Describe("a failed provision job and a spec change", func() {
		requestRestart := func(f *ciFixture) {
			GinkgoHelper()
			Eventually(func() error {
				ci := &osacv1alpha1.ComputeInstance{}
				if err := k8sClient.Get(ctx, f.key, ci); err != nil {
					return err
				}
				now := metav1.NewTime(time.Now().Truncate(time.Second))
				if ci.Spec.RestartRequestedAt != nil {
					now = metav1.NewTime(ci.Spec.RestartRequestedAt.Add(time.Hour))
				}
				ci.Spec.RestartRequestedAt = &now
				return k8sClient.Update(ctx, ci)
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		}

		// failedFixture drives a Create whose provision job failed finally,
		// before any VM exists.
		failedFixture := func(name string) *ciFixture {
			GinkgoHelper()
			f := newFixture(name, true)
			createNamespace(name + "-net")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
			})
			setAttachmentStatus(f, attached(name+"-net", metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs).To(HaveLen(1))
			})
			f.aap.finish(computeInstanceProvisionTask, "failed", `{"osac_error": {"message": "no capacity", "retryable": false}}`)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
			})
			return f
		}

		// resize changes the spec and the instance type label, as the Fulfillment Service does.
		resize := func(f *ciFixture, vcpus int32, typeName string) {
			GinkgoHelper()
			Eventually(func() error {
				ci := &osacv1alpha1.ComputeInstance{}
				if err := k8sClient.Get(ctx, f.key, ci); err != nil {
					return err
				}
				ci.Spec.VCPUs = vcpus
				ci.Labels[osacComputeInstanceTypeNameLabel] = typeName
				return k8sClient.Update(ctx, ci)
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		}

		// runningFixture drives a ComputeInstance to a Ready, Running VM whose running
		// configuration is recorded for the instance type "std-4".
		runningFixture := func(name string) *ciFixture {
			GinkgoHelper()
			f := newFixture(name, true)
			vmNamespace := name + "-net"
			createNamespace(vmNamespace)
			createSubnet(f, []string{"10.9.0.0/25"})
			Eventually(func() error {
				ci := &osacv1alpha1.ComputeInstance{}
				if err := k8sClient.Get(ctx, f.key, ci); err != nil {
					return err
				}
				ci.Labels[osacComputeInstanceTypeNameLabel] = "std-4"
				return k8sClient.Update(ctx, ci)
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
			})
			setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs).To(HaveLen(1))
			})
			f.aap.finish(computeInstanceProvisionTask, "successful", "")
			createVM(f, vmNamespace, kubevirtv1.Running,
				kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "tenant", IPs: []string{"10.9.0.20"}})
			setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionTrue, osacv1alpha1.ReasonSucceeded, ""))
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseRunning))
				g.Expect(ci.Status.RunningInstanceTypeName).To(Equal("std-4"))
				g.Expect(ci.Status.RunningConfigSince).NotTo(BeNil())
			})
			return f
		}

		It("keeps the running configuration through a failed resize and moves it only when a new spec is applied", func() {
			f := runningFixture("vmaas-resize")
			before := mustGetCI(f.key)
			since := before.Status.RunningConfigSince.DeepCopy()

			resize(f, 8, "std-8")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
			})
			f.aap.finish(computeInstanceProvisionTask, "failed", `{"osac_error": {"message": "resize failed", "retryable": false}}`)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned).Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseRunning))
				g.Expect(ci.Status.RunningInstanceTypeName).To(Equal("std-4"))
				g.Expect(ci.Status.RunningConfigSince.Equal(since)).To(BeTrue())
			})

			resize(f, 16, "std-16")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(3)))
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
				c := condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(c.Reason).To(Equal(osacv1alpha1.ReasonInfrastructureReady))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionConfigurationApplied).Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseRunning))
				g.Expect(ci.Status.RunningInstanceTypeName).To(Equal("std-4"))
				g.Expect(ci.Status.RunningConfigSince.Equal(since)).To(BeTrue())
			})

			// Stamps have second precision: let the observation fall in a later second.
			time.Sleep(1100 * time.Millisecond)
			f.aap.finish(computeInstanceProvisionTask, "successful", "")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionConfigurationApplied).Status).To(Equal(metav1.ConditionTrue))
				g.Expect(ci.Status.RunningInstanceTypeName).To(Equal("std-16"))
				g.Expect(ci.Status.RunningConfigVersion).To(Equal(ci.Status.DesiredConfigVersion))
				g.Expect(ci.Status.RunningConfigSince.After(since.Time)).To(BeTrue(), "since %v new %v", since, ci.Status.RunningConfigSince)
			})
		})

		It("reruns after a failed resize is reverted and does not move the running configuration time", func() {
			f := runningFixture("vmaas-revert")
			since := mustGetCI(f.key).Status.RunningConfigSince.DeepCopy()

			resize(f, 8, "std-8")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
			})
			f.aap.finish(computeInstanceProvisionTask, "failed", `{"osac_error": {"message": "resize failed", "retryable": false}}`)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
			})

			resize(f, 4, "std-4")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(3)))
			})
			f.aap.finish(computeInstanceProvisionTask, "successful", "")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionConfigurationApplied).Status).To(Equal(metav1.ConditionTrue))
				g.Expect(ci.Status.RunningInstanceTypeName).To(Equal("std-4"))
				g.Expect(ci.Status.RunningConfigSince.Equal(since)).To(BeTrue())
			})
		})

		It("runs the job again as the next attempt on a new spec and recovers the status", func() {
			f := failedFixture("vmaas-rerun")
			requestRestart(f)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
				g.Expect(ci.Status.ProvisionConfigVersion).To(Equal(ci.Status.DesiredConfigVersion))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned).Reason).NotTo(Equal(osacv1alpha1.ReasonProvisioningFailed))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseStarting))
			})
			Expect(f.aap.tasks()).To(HaveLen(2))

			f.aap.finish(computeInstanceProvisionTask, "successful", "")
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateSucceeded))
				g.Expect(ci.Status.Phase).NotTo(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned).Reason).NotTo(Equal(osacv1alpha1.ReasonProvisioningFailed))
			})
		})

		It("keeps the failure final on the same spec", func() {
			f := failedFixture("vmaas-samespec")
			Consistently(func(g Gomega) {
				_, _ = f.r.Reconcile(ctx, request(f.key))
				ci := mustGetCI(f.key)
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(1)))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
			}, time.Second, 200*time.Millisecond).Should(Succeed())
			Expect(f.aap.tasks()).To(HaveLen(1))
		})

		It("does not loop when the rerun fails again", func() {
			f := failedFixture("vmaas-rerunfail")
			requestRestart(f)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
			})
			f.aap.finish(computeInstanceProvisionTask, "failed", `{"osac_error": {"message": "still no capacity", "retryable": false}}`)
			reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
				g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionProvisioned).Message).To(Equal("still no capacity"))
			})
			Consistently(func(g Gomega) {
				_, _ = f.r.Reconcile(ctx, request(f.key))
				ci := mustGetCI(f.key)
				g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
				g.Expect(ci.Status.Jobs[0].Attempt).To(Equal(int32(2)))
			}, time.Second, 200*time.Millisecond).Should(Succeed())
			Expect(f.aap.tasks()).To(HaveLen(2))
		})
	})

	It("retries a provision job that ended with an AAP error", func() {
		f := newFixture("vmaas-proverror", true)
		createNamespace("vmaas-proverror-net")
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached("vmaas-proverror-net", metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
		})
		f.aap.finish(computeInstanceProvisionTask, "error", "")
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRetrying))
			g.Expect(ci.Status.Phase).NotTo(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
		})
	})

	It("uses the Tenant namespace and the default pod network with provider networking disabled", func() {
		f := newFixture("vmaas-disabled", false)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
			c := condition(ci, osacv1alpha1.ComputeInstanceConditionNetworkReady)
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(c.Reason).To(Equal(osacv1alpha1.ReasonProvisioningDisabled))
		})
		Expect(f.aap.last(computeInstanceProvisionTask).vars["vm_network"]).To(Equal(map[string]any{
			"namespace":  ns,
			"networks":   []any{map[string]any{"name": "default", "pod": map[string]any{}}},
			"interfaces": []any{map[string]any{"name": "default", "masquerade": map[string]any{}}},
			"labels":     map[string]any{computeInstanceIDVMLabel: f.id},
		}))
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns,
			Name: osacv1alpha1.NetworkAttachmentName(osacv1alpha1.NetworkAttachmentTargetComputeInstance, f.id)}, &osacv1alpha1.NetworkAttachment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no NetworkAttachment with provider networking disabled")

		f.aap.finish(computeInstanceProvisionTask, "successful", "")
		createVM(f, ns, kubevirtv1.Running,
			kubevirtv1.VirtualMachineInstanceNetworkInterface{Name: "default", IP: "10.128.4.7"})
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.IPAddress).To(Equal("10.128.4.7"))
			g.Expect(ci.Status.ComputeNetworkAttachmentStatuses).To(BeEmpty())
			g.Expect(condition(ci, osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
		})
	})

	It("cancels the provision job, deprovisions, then deletes the NetworkAttachment", func() {
		f := newFixture("vmaas-delete", true)
		vmNamespace := "vmaas-delete-net"
		createNamespace(vmNamespace)
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Conditions).NotTo(BeEmpty())
		})
		setAttachmentStatus(f, attached(vmNamespace, metav1.ConditionFalse, osacv1alpha1.ReasonProgressing, ""))
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Jobs).To(HaveLen(1))
			g.Expect(ci.Status.Jobs[0].JobID).NotTo(BeEmpty())
		})
		provisionJob := f.aap.last(computeInstanceProvisionTask)

		Expect(k8sClient.Delete(ctx, &osacv1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Name: f.key.Name, Namespace: ns}})).To(Succeed())
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseDeleting))
			g.Expect(networkJob(ci, computeInstanceProvisionTask).State).To(Equal(osacv1alpha1.NetworkJobStateCancelled))
			g.Expect(networkJob(ci, computeInstanceDeprovisionTask)).NotTo(BeNil())
		})
		Expect(f.aap.cancelled).To(ContainElement(strconv.Itoa(provisionJob.id)))
		deprovision := f.aap.last(computeInstanceDeprovisionTask)
		Expect(deprovision.template).To(Equal("osac-delete-compute-instance"))
		Expect(deprovision.vars["vm_network"]).To(HaveKeyWithValue("namespace", vmNamespace))
		Expect(networkAttachmentOf(f).DeletionTimestamp).To(BeNil(), "the NetworkAttachment goes after the VM")

		f.aap.finish(computeInstanceDeprovisionTask, "successful", "")
		Eventually(func(g Gomega) {
			_, _ = f.r.Reconcile(ctx, request(f.key))
			err := k8sClient.Get(ctx, f.key, &osacv1alpha1.ComputeInstance{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns,
			Name: osacv1alpha1.NetworkAttachmentName(osacv1alpha1.NetworkAttachmentTargetComputeInstance, f.id)}, &osacv1alpha1.NetworkAttachment{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("keeps the finalizer while an automatic ExternalIP of the VM exists and deletes none", func() {
		f := newFixture("vmaas-autoeip", false)
		eip := &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "auto-eip-" + f.id,
				Namespace: ns,
				Labels:    map[string]string{autoCreatedLabel: labelValueTrue, autoCreatedForLabel: f.id},
			},
			Spec: osacv1alpha1.ExternalIPSpec{Pool: "pool"},
		}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, eip) })
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
		})
		Expect(k8sClient.Delete(ctx, &osacv1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Name: f.key.Name, Namespace: ns}})).To(Succeed())
		reconcileUntil(f, func(g Gomega, ci *osacv1alpha1.ComputeInstance) {
			g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseDeleting))
		})
		Consistently(func(g Gomega) {
			_, _ = f.r.Reconcile(ctx, request(f.key))
			ci := &osacv1alpha1.ComputeInstance{}
			g.Expect(k8sClient.Get(ctx, f.key, ci)).To(Succeed())
			g.Expect(ci.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
			current := &osacv1alpha1.ExternalIP{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), current)).To(Succeed())
			g.Expect(current.DeletionTimestamp).To(BeNil())
		}, time.Second, 200*time.Millisecond).Should(Succeed())
		Expect(f.aap.tasks()).NotTo(ContainElement(computeInstanceDeprovisionTask))

		Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
		Eventually(func(g Gomega) {
			_, _ = f.r.Reconcile(ctx, request(f.key))
			g.Expect(networkJob(mustGetCI(f.key), computeInstanceDeprovisionTask)).NotTo(BeNil())
		}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
	})
})

func mustGetCI(key types.NamespacedName) *osacv1alpha1.ComputeInstance {
	ci := &osacv1alpha1.ComputeInstance{}
	if err := k8sClient.Get(context.Background(), key, ci); err != nil {
		return &osacv1alpha1.ComputeInstance{}
	}
	return ci
}

func networkJob(ci *osacv1alpha1.ComputeInstance, task string) *osacv1alpha1.NetworkJobStatus {
	for i := range ci.Status.Jobs {
		if ci.Status.Jobs[i].Task == task {
			return &ci.Status.Jobs[i]
		}
	}
	return nil
}

var _ = Describe("ComputeInstance address selection", func() {
	vmi := &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{
		Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
			{Name: "guest", IPs: []string{"10.9.0.9"}},
			{Name: "tenant", IP: "10.9.0.30", IPs: []string{"fd00::1", "010.9.0.3", "192.0.2.1", "10.9.0.21"}},
		},
	}}

	It("takes the first canonical IPv4 address of the named interface in the range", func() {
		Expect(firstIPv4OfInterface(vmi, "tenant", []string{"10.9.0.0/25"})).To(Equal("10.9.0.21"))
	})

	It("ignores other interfaces", func() {
		Expect(firstIPv4OfInterface(vmi, "tenant", []string{"10.9.0.8/29"})).To(BeEmpty())
		Expect(firstIPv4OfInterface(vmi, "default", nil)).To(BeEmpty())
	})

	It("takes any IPv4 address without a range", func() {
		Expect(firstIPv4OfInterface(vmi, "tenant", nil)).To(Equal("192.0.2.1"))
	})

	It("reads the bound address from the NetworkAttachment task input", func() {
		na := &osacv1alpha1.NetworkAttachment{}
		Expect(boundIPv4Address(na)).To(BeEmpty())
		na.Status.TaskInput = &runtime.RawExtension{Raw: []byte(`{"network_attachment": {"interface": {"type": "overlay", "ipv4_address": "10.9.0.21"}}}`)}
		Expect(boundIPv4Address(na)).To(Equal("10.9.0.21"))
	})
})

var _ = Describe("ComputeInstance validated address kept without a range", func() {
	vmiWith := func(addresses ...string) *kubevirtv1.VirtualMachineInstance {
		return &kubevirtv1.VirtualMachineInstance{Status: kubevirtv1.VirtualMachineInstanceStatus{
			Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{{Name: "tenant", IPs: addresses}},
		}}
	}
	instanceWith := func(subnet, address string) *osacv1alpha1.ComputeInstance {
		ci := &osacv1alpha1.ComputeInstance{}
		if address != "" {
			ci.Status.ComputeNetworkAttachmentStatuses = []osacv1alpha1.ComputeNetworkAttachmentStatus{{SubnetRef: subnet, IPAddress: address}}
		}
		return ci
	}

	It("keeps the address of the same Subnet that the VM still reports", func() {
		Expect(keepsValidatedAddress(instanceWith("subnet-a", "10.9.0.20"), vmiWith("10.9.0.20"), "subnet-a")).To(BeTrue())
	})

	It("does not keep an address validated for another Subnet", func() {
		Expect(keepsValidatedAddress(instanceWith("subnet-b", "10.9.0.20"), vmiWith("10.9.0.20"), "subnet-a")).To(BeFalse())
	})

	It("does not keep an address the VM no longer reports", func() {
		Expect(keepsValidatedAddress(instanceWith("subnet-a", "10.9.0.20"), vmiWith("10.9.0.30"), "subnet-a")).To(BeFalse())
	})

	It("keeps nothing without a validated address", func() {
		Expect(keepsValidatedAddress(instanceWith("subnet-a", ""), vmiWith("10.9.0.20"), "subnet-a")).To(BeFalse())
	})
})
