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
	"path/filepath"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	metal3TestNS    = "metal3-test"
	metal3HostClass = "metal3"
)

func createMetal3BMH(name string, labels map[string]string, opStatus metal3api.OperationalStatus, provState metal3api.ProvisioningState) *metal3api.BareMetalHost {
	if labels == nil {
		labels = map[string]string{}
	}
	bmh := &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metal3TestNS,
			Labels:    labels,
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, bmh)).To(Succeed())

	// Status is a subresource — Create does not persist it. Re-set and update
	// separately so envtest stores the desired operational/provisioning state.
	// HardwareDetails is required so FindFreeHost does not skip this host.
	bmh.Status.OperationalStatus = opStatus
	bmh.Status.Provisioning.State = provState
	bmh.Status.HardwareDetails = &metal3api.HardwareDetails{
		NIC: []metal3api.NIC{{MAC: "aa:bb:cc:dd:ee:01"}},
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, bmh)).To(Succeed())
	return bmh
}

func newMetal3Reconciler() *BareMetalInstanceReconciler {
	invClient := inventory.NewMetal3ClientForTest(k8sClient, metal3TestNS, metal3HostClass)
	mgmtClient := management.NewMetal3ClientForTest(k8sClient, metal3TestNS)
	return NewBareMetalInstanceReconciler(
		k8sClient,
		k8sClient.Scheme(),
		invClient,
		mgmtClient,
		nil, // provisioning provider
		nil, // networking provider
		nil, // IP discovery provider
		nil, // AAP client
		0, 0, 0, 0,
	)
}

func reconcileN(reconciler *BareMetalInstanceReconciler, name string, n int) ctrl.Result {
	var result ctrl.Result
	for range n {
		var err error
		result, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: metal3TestNS},
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
	return result
}

func getBMI(name string) *v1alpha1.BareMetalInstance {
	bmi := &v1alpha1.BareMetalInstance{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: metal3TestNS}, bmi)).To(Succeed())
	return bmi
}

func getBMH(name string) *metal3api.BareMetalHost {
	bmh := &metal3api.BareMetalHost{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: metal3TestNS}, bmh)).To(Succeed())
	return bmh
}

// cleanupBMI removes a BareMetalInstance, stripping finalizers first since no
// controller is running in envtest to handle them.
func cleanupBMI(name string) {
	bmi := &v1alpha1.BareMetalInstance{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: metal3TestNS}, bmi); err != nil {
		ExpectWithOffset(1, client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		return
	}
	if len(bmi.Finalizers) > 0 {
		bmi.Finalizers = nil
		ExpectWithOffset(1, k8sClient.Update(ctx, bmi)).To(Succeed())
	}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, bmi))).NotTo(HaveOccurred())
}

func cleanupBMH(name string) {
	bmh := &metal3api.BareMetalHost{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: metal3TestNS}, bmh); err != nil {
		ExpectWithOffset(1, client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		return
	}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, bmh))).NotTo(HaveOccurred())
}

var _ = Describe("BareMetalInstance Metal3 Integration", func() {
	var ns *corev1.Namespace

	BeforeEach(func() {
		ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: metal3TestNS}}
		err := k8sClient.Create(ctx, ns)
		if err != nil && client.IgnoreAlreadyExists(err) != nil {
			Fail("failed to create test namespace: " + err.Error())
		}
	})

	Describe("Allocation flow", func() {
		const bmiName = "alloc-test-bmi"
		const bmhName = "alloc-test-bmh"

		AfterEach(func() {
			cleanupBMI(bmiName)
			cleanupBMH(bmhName)
		})

		It("should allocate a BMH and transition to Progressing", func() {
			createMetal3BMH(bmhName, map[string]string{
				inventory.Metal3HostTypeLabel: "gpu-node",
			}, metal3api.OperationalStatusOK, metal3api.StateAvailable)

			bmi := &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType:   "gpu-node",
					TemplateID: shared.OsacNoopTemplate,
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			reconciler := newMetal3Reconciler()

			// Reconcile 1: add inventory finalizer
			reconcileN(reconciler, bmiName, 1)
			bmi = getBMI(bmiName)
			Expect(bmi.Finalizers).To(ContainElement(BareMetalInstanceInventoryFinalizer))

			// Reconcile 2: FindFreeHost → set ExternalHostID
			reconcileN(reconciler, bmiName, 1)
			bmi = getBMI(bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(metal3TestNS + "/" + bmhName))

			// Reconcile 3: AssignHost → set HostClass, status persisted as Progressing
			reconcileN(reconciler, bmiName, 1)
			bmi = getBMI(bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(metal3HostClass))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))

			// Verify BMH has consumerRef set
			updatedBMH := getBMH(bmhName)
			Expect(updatedBMH.Spec.ConsumerRef).NotTo(BeNil())
			Expect(updatedBMH.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))
		})
	})

	Describe("Power management flow", func() {
		const bmiName = "power-test-bmi"
		const bmhName = "power-test-bmh"

		AfterEach(func() {
			cleanupBMI(bmiName)
			cleanupBMH(bmhName)
		})

		It("should manage power state and transition to Ready", func() {
			createMetal3BMH(bmhName, map[string]string{
				inventory.Metal3HostTypeLabel: "compute",
			}, metal3api.OperationalStatusOK, metal3api.StateAvailable)

			bmi := &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType:    "compute",
					TemplateID:  shared.OsacNoopTemplate,
					RunStrategy: v1alpha1.RunStrategyAlways,
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			reconciler := newMetal3Reconciler()

			// Drive through allocation (3 reconciles: finalizer, find, assign)
			reconcileN(reconciler, bmiName, 3)
			bmi = getBMI(bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(metal3HostClass))

			// Reconcile: add management finalizer
			reconcileN(reconciler, bmiName, 1)
			bmi = getBMI(bmiName)
			Expect(bmi.Finalizers).To(ContainElement(BareMetalInstanceManagementFinalizer))

			// Reconcile: GetPowerState (off) → SetPowerState (on) → requeue
			result := reconcileN(reconciler, bmiName, 1)
			Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))

			// Verify BMH spec.online was patched to true
			updatedBMH := getBMH(bmhName)
			Expect(updatedBMH.Spec.Online).To(BeTrue())

			// Simulate BMO reconciliation: update status.poweredOn
			updatedBMH.Status.PoweredOn = true
			Expect(k8sClient.Status().Update(ctx, updatedBMH)).To(Succeed())

			// Reconcile twice: first verifies convergence (stale-read guard), second reaches Ready
			reconcileN(reconciler, bmiName, 2)
			bmi = getBMI(bmiName)
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			Expect(bmi.Status.RunStrategy).To(Equal(v1alpha1.RunStrategyAlways))

			condition := bmi.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(v1alpha1.HostConditionReasonPowerOn))
		})
	})

	Describe("Deallocation flow", func() {
		const bmiName = "dealloc-test-bmi"
		const bmhName = "dealloc-test-bmh"

		AfterEach(func() {
			cleanupBMI(bmiName)
			cleanupBMH(bmhName)
		})

		It("should unassign the BMH and remove finalizers on deletion", func() {
			createMetal3BMH(bmhName, map[string]string{
				inventory.Metal3HostTypeLabel: "storage",
			}, metal3api.OperationalStatusOK, metal3api.StateAvailable)

			bmi := &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType:   "storage",
					TemplateID: shared.OsacNoopTemplate,
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			reconciler := newMetal3Reconciler()

			// Drive through allocation and management setup
			// 3 reconciles for allocation + 1 for management finalizer + 1 for ready
			reconcileN(reconciler, bmiName, 5)
			bmi = getBMI(bmiName)
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			Expect(bmi.Finalizers).To(ContainElement(BareMetalInstanceInventoryFinalizer))
			Expect(bmi.Finalizers).To(ContainElement(BareMetalInstanceManagementFinalizer))

			// Verify BMH is assigned
			updatedBMH := getBMH(bmhName)
			Expect(updatedBMH.Spec.ConsumerRef).NotTo(BeNil())

			// Delete the BareMetalInstance
			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())

			// Reconcile: management finalizer removed (noop template → skip deprovision),
			// inventory cleanup: unassign + remove inventory finalizer — all in one reconcile
			reconcileN(reconciler, bmiName, 1)

			// Verify BMH is unassigned
			updatedBMH = getBMH(bmhName)
			Expect(updatedBMH.Spec.ConsumerRef).To(BeNil())

			// Verify the BareMetalInstance is gone (finalizers removed → k8s garbage collects)
			err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: metal3TestNS}, &v1alpha1.BareMetalInstance{})
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})

	Describe("Error cases", func() {
		Describe("no matching BMH available", func() {
			const bmiName = "no-host-bmi"

			AfterEach(func() {
				cleanupBMI(bmiName)
			})

			It("should set Failed phase and requeue", func() {
				bmi := &v1alpha1.BareMetalInstance{
					ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
					Spec: v1alpha1.BareMetalInstanceSpec{
						HostType:   "nonexistent-type",
						TemplateID: shared.OsacNoopTemplate,
					},
				}
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				reconciler := newMetal3Reconciler()

				// Reconcile 1: add finalizer
				reconcileN(reconciler, bmiName, 1)

				// Reconcile 2: FindFreeHost returns nil → Failed
				result := reconcileN(reconciler, bmiName, 1)
				Expect(result.RequeueAfter).To(Equal(DefaultNoFreeHostsPollIntervalDuration))

				bmi = getBMI(bmiName)
				Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			})
		})

		Describe("BMH taken by concurrent assignment", func() {
			const bmiName = "taken-host-bmi"
			const bmhName = "taken-host-bmh"

			AfterEach(func() {
				cleanupBMI(bmiName)
				cleanupBMH(bmhName)
			})

			It("should unset ExternalHostID when BMH is already claimed", func() {
				bmh := createMetal3BMH(bmhName, map[string]string{
					inventory.Metal3HostTypeLabel: "contested",
				}, metal3api.OperationalStatusOK, metal3api.StateAvailable)
				bmh.Spec.ConsumerRef = &corev1.ObjectReference{
					APIVersion: "osac.openshift.io/v1alpha1",
					Kind:       "BareMetalInstance",
					Name:       "other-instance",
				}
				Expect(k8sClient.Update(ctx, bmh)).To(Succeed())

				bmi := &v1alpha1.BareMetalInstance{
					ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
					Spec: v1alpha1.BareMetalInstanceSpec{
						HostType:       "contested",
						TemplateID:     shared.OsacNoopTemplate,
						ExternalHostID: metal3TestNS + "/" + bmhName,
					},
				}
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				reconciler := newMetal3Reconciler()

				// Reconcile 1: add finalizer
				reconcileN(reconciler, bmiName, 1)

				// Reconcile 2: AssignHost returns nil (host taken) → ExternalHostID unset
				reconcileN(reconciler, bmiName, 1)
				bmi = getBMI(bmiName)
				Expect(bmi.Spec.ExternalHostID).To(BeEmpty())
			})
		})

		Describe("CRD discovery", func() {
			It("should detect BareMetalHost CRD via the discovery API", func() {
				dc, err := discovery.NewDiscoveryClientForConfig(cfg)
				Expect(err).NotTo(HaveOccurred())
				resources, err := dc.ServerResourcesForGroupVersion("metal3.io/v1alpha1")
				Expect(err).NotTo(HaveOccurred())
				Expect(resources).NotTo(BeNil())

				found := false
				for _, r := range resources.APIResources {
					if r.Kind == "BareMetalHost" {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "BareMetalHost should be discoverable via the API")
			})

			It("should fail when BareMetalHost CRD is not present", func() {
				// Start a minimal envtest without BMH CRDs
				miniEnv := &envtest.Environment{
					CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
					ErrorIfCRDPathMissing: false,
					Scheme:                scheme.Scheme,
				}
				if getFirstFoundEnvTestBinaryDir() != "" {
					miniEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
				}
				miniCfg, err := miniEnv.Start()
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = miniEnv.Stop() }()

				dc, err := discovery.NewDiscoveryClientForConfig(miniCfg)
				Expect(err).NotTo(HaveOccurred())
				_, err = dc.ServerResourcesForGroupVersion("metal3.io/v1alpha1")
				Expect(err).To(HaveOccurred(), "metal3.io/v1alpha1 should not be available without BMH CRD")
			})
		})
	})

	Describe("NIC metadata flow", func() {
		const bmiName = "nic-test-bmi"
		const bmhName = "nic-test-bmh"

		AfterEach(func() {
			cleanupBMI(bmiName)
			cleanupBMH(bmhName)
		})

		It("should populate status.hardware.nics from BareMetalHost hardware inspection", func() {
			bmh := createMetal3BMH(bmhName, map[string]string{
				inventory.Metal3HostTypeLabel: "gpu-node",
			}, metal3api.OperationalStatusOK, metal3api.StateAvailable)
			_ = bmh

			bmi := &v1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{Name: bmiName, Namespace: metal3TestNS},
				Spec: v1alpha1.BareMetalInstanceSpec{
					HostType:   "gpu-node",
					TemplateID: shared.OsacNoopTemplate,
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			reconciler := newMetal3Reconciler()

			// 3 reconciles for allocation + 1 for management finalizer + 1 for Ready
			reconcileN(reconciler, bmiName, 5)
			bmi = getBMI(bmiName)

			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))
			Expect(bmi.Status.Hardware).NotTo(BeNil())
			Expect(bmi.Status.Hardware.NICs).To(HaveLen(1))
			Expect(bmi.Status.Hardware.NICs[0].MAC).To(Equal("aa:bb:cc:dd:ee:01"))
		})
	})
})
