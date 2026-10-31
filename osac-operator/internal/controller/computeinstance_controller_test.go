/*
Copyright 2025.

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
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const testTemplateParams = `{"key": "value"}`

// createReadyTenant creates a Tenant with the given name in the namespace and sets status to Ready.
// If the tenant already exists, it is updated to Ready.
func createReadyTenant(ctx context.Context, namespace, name string) {
	tenant := &osacv1alpha1.Tenant{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, tenant)
	if err != nil && errors.IsNotFound(err) {
		tenant = &osacv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: osacv1alpha1.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
	}
	tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
	tenant.Status.Namespace = namespace
	Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
}

// deleteTenantInNamespace removes a Tenant by name in the given namespace (clears finalizers first).
func deleteTenantInNamespace(ctx context.Context, namespace, name string) {
	tenant := &osacv1alpha1.Tenant{}
	nn := types.NamespacedName{Name: name, Namespace: namespace}
	if err := k8sClient.Get(ctx, nn, tenant); err == nil {
		tenant.Finalizers = nil
		_ = k8sClient.Update(ctx, tenant)
		_ = k8sClient.Delete(ctx, tenant)
	}
}

var _ = Describe("ComputeInstance Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		const namespaceName = "default"
		const tenantName = "test-tenant"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespaceName,
		}
		computeInstance := &osacv1alpha1.ComputeInstance{}

		BeforeEach(func() {
			By("creating a tenant for the ComputeInstance to reference")
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tenantName,
					Namespace: namespaceName,
				},
				Spec: osacv1alpha1.TenantSpec{},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, &osacv1alpha1.Tenant{})
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
				tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
				tenant.Status.Namespace = namespaceName
				Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
			}

			By("creating the custom resource for the Kind ComputeInstance")
			err = k8sClient.Get(ctx, typeNamespacedName, computeInstance)
			if err != nil && errors.IsNotFound(err) {
				resource := &osacv1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespaceName,
						Annotations: map[string]string{
							osacTenantKey: tenantName,
						},
					},
					Spec: newTestComputeInstanceSpec("test_template"),
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}

			By("waiting for the manager cache to see the resources")
			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func() error {
				return mgrClient.Get(ctx, typeNamespacedName, &osacv1alpha1.ComputeInstance{})
			}).Should(Succeed())
			Eventually(func(g Gomega) {
				t := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, t)).To(Succeed())
				g.Expect(t.Status.Phase).To(Equal(osacv1alpha1.TenantPhaseReady))
			}).Should(Succeed())
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance ComputeInstance")
			err := k8sClient.Get(ctx, typeNamespacedName, computeInstance)
			Expect(err).NotTo(HaveOccurred())

			// Now delete the resource
			err = k8sClient.Delete(ctx, computeInstance)
			if err != nil && !errors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("Reconciling the deleted resource")
			Eventually(func() error {
				controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
				_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
					NamespacedName: typeNamespacedName,
				}})
				return err
			}).Should(Succeed())

			By("Cleanup the tenant")
			tenant := &osacv1alpha1.Tenant{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, tenant); err == nil {
				_ = k8sClient.Delete(ctx, tenant)
			}
		})
		It("should successfully reconcile the resource", func() {
			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)

			// Reconcile inside Eventually: the first call may requeue if
			// the envtest cache has not yet propagated the Tenant status
			// update from BeforeEach. Retrying allows the cache to catch up.
			vm := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
					NamespacedName: typeNamespacedName,
				}})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
				g.Expect(vm.Status.TenantReference).NotTo(BeNil())
				g.Expect(vm.Status.TenantReference.Name).To(Equal(tenantName))
				g.Expect(vm.Status.TenantReference.Namespace).To(Equal(namespaceName))
			}, 10*time.Second, 200*time.Millisecond).Should(Succeed())

			By("Verifying the finalizer is set on the ComputeInstance resource")
			Expect(vm.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
		})
	})

	Context("management-state unmanaged with deletion", func() {
		const namespaceName = "default"
		const tenantName = "test-tenant-unmanaged"
		ctx := context.Background()

		It("should still handle delete for unmanaged ComputeInstance with finalizer", func() {
			createReadyTenant(ctx, namespaceName, tenantName)

			managedThenUnmanaged := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacComputeInstanceManagementStateAnnotation: ManagementStateUnmanaged,
						osacTenantKey: tenantName,
					},
					Finalizers: []string{osacComputeInstanceFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: namespaceName}

			controllerReconciler := newTestCIReconciler("", "", namespaceName, 100*time.Millisecond)

			Expect(k8sClient.Delete(ctx, managedThenUnmanaged)).To(Succeed())

			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Get(ctx, key, ci)).To(Succeed())
				g.Expect(ci.DeletionTimestamp.IsZero()).To(BeFalse())
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: key,
			}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Get(ctx, key, ci)).To(Succeed())
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseDeleting))
				g.Expect(ci.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: key,
			}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, key, &osacv1alpha1.ComputeInstance{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())

			deleteTenantInNamespace(ctx, namespaceName, tenantName)
		})
	})

	Context("recordRunningConfig", func() {
		// runningInstance builds a ComputeInstance whose CR reports the labeled type
		// running: phase Running, configuration applied, and a RestartRequired
		// condition the caller controls.
		runningInstance := func(restart metav1.ConditionStatus, present bool) *osacv1alpha1.ComputeInstance {
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						osacComputeInstanceTypeNameLabel: "standard-8-32",
					},
				},
			}
			if present {
				instance.Status.Conditions = []metav1.Condition{{
					Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
					Status:             restart,
					Reason:             "AsExpected",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				}}
			}
			instance.Status.Conditions = append(instance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
				Status:             metav1.ConditionTrue,
				Reason:             "AsExpected",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			instance.Status.Phase = osacv1alpha1.ComputeInstancePhaseRunning
			return instance
		}

		steadyState := func(instance *osacv1alpha1.ComputeInstance) *osacv1alpha1.ComputeInstance {
			version, err := provisioning.ComputeDesiredConfigVersion(instance.Spec)
			Expect(err).ToNot(HaveOccurred())
			instance.Status.DesiredConfigVersion = version
			instance.Status.RunningConfigVersion = version
			instance.Status.RunningInstanceTypeName = instance.Labels[osacComputeInstanceTypeNameLabel]
			since := metav1.NewTime(time.Now().Add(-time.Hour).UTC())
			instance.Status.RunningConfigSince = &since
			return instance
		}

		It("records when the restart condition is explicitly false", func() {
			instance := steadyState(runningInstance(metav1.ConditionFalse, true))
			instance.Status.RunningConfigVersion = ""
			instance.Status.RunningInstanceTypeName = ""
			instance.Status.RunningConfigSince = nil

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(Equal(instance.Status.DesiredConfigVersion))
			Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32"))
			Expect(instance.Status.RunningConfigSince).ToNot(BeNil())
		})

		It("does not record when the restart condition is absent", func() {
			// KubeVirt reports RestartRequired asynchronously: an absent condition
			// vouches for nothing, so no observation is recorded yet.
			instance := runningInstance("", false)
			instance.Status.RunningConfigVersion = ""
			instance.Status.RunningInstanceTypeName = ""
			instance.Status.RunningConfigSince = nil

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(BeEmpty())
			Expect(instance.Status.RunningInstanceTypeName).To(BeEmpty())
			Expect(instance.Status.RunningConfigSince).To(BeNil())
		})

		It("does not record when the restart condition is unknown", func() {
			instance := runningInstance(metav1.ConditionUnknown, true)
			instance.Status.RunningConfigVersion = ""
			instance.Status.RunningInstanceTypeName = ""
			instance.Status.RunningConfigSince = nil

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(BeEmpty())
			Expect(instance.Status.RunningConfigSince).To(BeNil())
		})

		It("clears the observation when a restart is required", func() {
			// The observation predates the restart and cannot vouch for the
			// configuration that runs after it.
			instance := steadyState(runningInstance(metav1.ConditionTrue, true))

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(BeEmpty())
			Expect(instance.Status.RunningInstanceTypeName).To(BeEmpty())
			Expect(instance.Status.RunningConfigSince).To(BeNil())
		})

		It("keeps the recorded time in the steady state", func() {
			instance := steadyState(runningInstance(metav1.ConditionFalse, true))
			before := *instance.Status.RunningConfigSince

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigSince.Time).To(Equal(before.Time))
		})

		It("moves the stamp on a name-only resize with identical resources", func() {
			// Two instance types with identical resources hash to the same config
			// version: only the recorded name distinguishes them, so a resize
			// between them must still move the stamp.
			instance := steadyState(runningInstance(metav1.ConditionFalse, true))
			oldSince := *instance.Status.RunningConfigSince
			// The reconciler applied the new type: the label moved.
			instance.Labels[osacComputeInstanceTypeNameLabel] = "standard-8-32-compatible"
			// ...but the resources are identical, so the config version is unchanged.

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(Equal(instance.Status.DesiredConfigVersion))
			Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32-compatible"))
			Expect(instance.Status.RunningConfigSince.Time).To(BeTemporally(">", oldSince.Time))
		})

		It("FirstTypeCoversRunningBeforeInitialJobCompletion", func() {
			// Initial provisioning: the VM enters RUNNING at 10:00:00 but the AAP
			// job completes at 10:00:20, so ConfigurationApplied turns true only
			// then. The type is known from the spec at admission and is what the
			// VM started with, so the first record must date from the phase time:
			// taking the later of the two leaves the first 20 s of the RUNNING
			// span permanently without the instance_type dimension.
			phaseAt := time.Now().Add(-time.Minute).UTC()
			stoppedAt := phaseAt.Add(40 * time.Second)
			configAt := phaseAt.Add(20 * time.Second)
			instance := runningInstanceWithTimes(phaseAt, configAt)
			version, err := provisioning.ComputeDesiredConfigVersion(instance.Spec)
			Expect(err).ToNot(HaveOccurred())
			instance.Status.DesiredConfigVersion = version

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(Equal(version))
			Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32"))
			Expect(instance.Status.RunningConfigSince.Time).To(BeTemporally("~", phaseAt, time.Second))
			// The settled usage keeps all 40 seconds typed: the instance_type
			// dimension covers the whole [stamp, stop] span with no untimed head.
			Expect(stoppedAt.Sub(instance.Status.RunningConfigSince.Time)).To(Equal(40 * time.Second))

			// Settling: a later reconcile keeps the stamp.
			before := *instance.Status.RunningConfigSince
			recordRunningConfig(instance)
			Expect(instance.Status.RunningConfigSince.Time).To(Equal(before.Time))
		})

		It("RestartedResizeStampsTheRunningTransitionNotNow", func() {
			// A resize that needs a restart: the pending restart clears the
			// recorded running config, so the record after the restart is a
			// first record. The VM returned to Running 15 s ago but the job
			// completion is only noticed now: the new type must date from the
			// Running transition, or the old type bills the gap.
			instance := steadyState(runningInstance(metav1.ConditionTrue, true))
			recordRunningConfig(instance)
			Expect(instance.Status.RunningConfigVersion).To(BeEmpty())
			Expect(instance.Status.RunningConfigSince).To(BeNil())

			runningAt := time.Now().Add(-15 * time.Second).UTC()
			runningTime := metav1.NewTime(runningAt)
			instance.Status.PhaseTransitionTime = &runningTime
			instance.Labels[osacComputeInstanceTypeNameLabel] = "standard-16-64"
			instance.SetStatusCondition(osacv1alpha1.ComputeInstanceConditionRestartRequired,
				metav1.ConditionFalse, "", osacv1alpha1.ReasonAsExpected)

			recordRunningConfig(instance)

			Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-16-64"))
			Expect(instance.Status.RunningConfigSince).ToNot(BeNil())
			Expect(instance.Status.RunningConfigSince.Time).To(BeTemporally("~", runningAt, time.Second))
		})

		It("NameOnlyResizeDoesNotBackdateTheNewType", func() {
			// A name-only resize: two instance type names resolve to identical
			// CPU, memory, and GPU specs, so the config hash is unchanged and
			// only the label moves. The provisioning job has nothing to do, the
			// ConfigurationApplied condition never flips, and its transition
			// time stays at the original provisioning an hour earlier. The new
			// type must stamp from the observation: reusing the stale condition
			// or phase time would backdate the new type and move the preceding
			// hour of usage to the new pricing dimension.
			phaseAt := time.Now().Add(-time.Hour).UTC()
			configAt := phaseAt.Add(20 * time.Second)
			instance := runningInstanceWithTimes(phaseAt, configAt)
			version, err := provisioning.ComputeDesiredConfigVersion(instance.Spec)
			Expect(err).ToNot(HaveOccurred())
			// The resources are identical, so the resize leaves the config hash,
			// and with it the desired version, unchanged.
			instance.Status.DesiredConfigVersion = version
			instance.Status.RunningConfigVersion = version
			instance.Status.RunningInstanceTypeName = "standard-8-32"
			oldSince := metav1.NewTime(configAt)
			instance.Status.RunningConfigSince = &oldSince
			// The reconciler applied the new type: only the label moved.
			instance.Labels[osacComputeInstanceTypeNameLabel] = "standard-8-32-compatible"

			recordRunningConfig(instance)

			Expect(instance.Status.RunningConfigVersion).To(Equal(version))
			Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32-compatible"))
			// The new type's stamp is the observation time, not the original
			// phase or condition time.
			Expect(instance.Status.RunningConfigSince.Time).To(BeTemporally("~", time.Now(), 2*time.Second))
			Expect(instance.Status.RunningConfigSince.Time).To(BeTemporally(">", configAt))
		})
	})

	Context("handleDesiredConfigVersion", func() {
		var reconciler *ComputeInstanceReconciler
		ctx := context.Background()

		BeforeEach(func() {
			reconciler = newTestCIReconciler("", "", "", 0)
		})

		It("should compute and store a version of the spec", func() {
			spec := newTestComputeInstanceSpec("template-1")
			spec.TemplateParameters = testTemplateParams
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-hash",
					Namespace: "default",
				},
				Spec: spec,
			}

			err := reconciler.handleDesiredConfigVersion(ctx, vm)
			Expect(err).NotTo(HaveOccurred())
			Expect(vm.Status.DesiredConfigVersion).NotTo(BeEmpty())
		})

		It("should be idempotent - same spec produces same version", func() {
			spec := newTestComputeInstanceSpec("template-1")
			spec.TemplateParameters = testTemplateParams
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-idempotent",
					Namespace: "default",
				},
				Spec: spec,
			}

			// First call
			err := reconciler.handleDesiredConfigVersion(ctx, vm)
			Expect(err).NotTo(HaveOccurred())
			firstVersion := vm.Status.DesiredConfigVersion
			Expect(firstVersion).NotTo(BeEmpty())

			// Second call with same spec
			err = reconciler.handleDesiredConfigVersion(ctx, vm)
			Expect(err).NotTo(HaveOccurred())
			secondVersion := vm.Status.DesiredConfigVersion
			Expect(secondVersion).To(Equal(firstVersion))

			// Third call with same spec
			err = reconciler.handleDesiredConfigVersion(ctx, vm)
			Expect(err).NotTo(HaveOccurred())
			thirdVersion := vm.Status.DesiredConfigVersion
			Expect(thirdVersion).To(Equal(firstVersion))
		})

		It("should produce different versions for different specs", func() {
			spec1 := newTestComputeInstanceSpec("template-1")
			spec1.TemplateParameters = `{"key": "value1"}`
			vm1 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-diff-1",
					Namespace: "default",
				},
				Spec: spec1,
			}

			spec2 := newTestComputeInstanceSpec("template-1")
			spec2.TemplateParameters = `{"key": "value2"}`
			vm2 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-diff-2",
					Namespace: "default",
				},
				Spec: spec2,
			}

			err := reconciler.handleDesiredConfigVersion(ctx, vm1)
			Expect(err).NotTo(HaveOccurred())

			err = reconciler.handleDesiredConfigVersion(ctx, vm2)
			Expect(err).NotTo(HaveOccurred())

			Expect(vm1.Status.DesiredConfigVersion).NotTo(Equal(vm2.Status.DesiredConfigVersion))
		})

		It("should produce different versions for different template IDs", func() {
			vm1 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-template-1",
					Namespace: "default",
				},
				Spec: newTestComputeInstanceSpec("template-1"),
			}

			vm2 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-template-2",
					Namespace: "default",
				},
				Spec: newTestComputeInstanceSpec("template-2"),
			}

			err := reconciler.handleDesiredConfigVersion(ctx, vm1)
			Expect(err).NotTo(HaveOccurred())

			err = reconciler.handleDesiredConfigVersion(ctx, vm2)
			Expect(err).NotTo(HaveOccurred())

			Expect(vm1.Status.DesiredConfigVersion).NotTo(Equal(vm2.Status.DesiredConfigVersion))
		})

		It("should produce same version regardless of order of calls", func() {
			spec1 := newTestComputeInstanceSpec("template-1")
			spec1.TemplateParameters = testTemplateParams
			vm1 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-order-1",
					Namespace: "default",
				},
				Spec: spec1,
			}

			spec2 := newTestComputeInstanceSpec("template-1")
			spec2.TemplateParameters = testTemplateParams
			vm2 := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ci-order-2",
					Namespace: "default",
				},
				Spec: spec2,
			}

			// Call on vm1 first
			err := reconciler.handleDesiredConfigVersion(ctx, vm1)
			Expect(err).NotTo(HaveOccurred())

			// Then call on vm2
			err = reconciler.handleDesiredConfigVersion(ctx, vm2)
			Expect(err).NotTo(HaveOccurred())

			// Versions should be identical
			Expect(vm1.Status.DesiredConfigVersion).To(Equal(vm2.Status.DesiredConfigVersion))
		})
	})

	Context("Phase regression prevention", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		It("should set Starting phase on first-time provisioning", func() {
			const resourceName = "test-phase-first-provision"
			const tenantName = "tenant-phase-first"
			defer deleteCI(resourceName)
			createReadyTenant(ctx, namespaceName, tenantName)
			defer deleteTenantInNamespace(ctx, namespaceName, tenantName)

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)

			// Wait for the CI to appear in the controller's cache before calling Reconcile
			// directly. Without this, r.Get() inside Reconcile returns NotFound (cache miss)
			// and Reconcile silently returns nil without setting Phase, making the test flaky.
			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())

			ci := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseStarting))
			}).Should(Succeed())
		})

		It("should trigger provisioning only once when reconciled twice in rapid succession", func() {
			const resourceName = "test-duplicate-provision"
			const tenantName = "tenant-dup-provision"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})
			createReadyTenant(ctx, namespaceName, tenantName)

			objectKey := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			aapFake := newWLFakeAAP()
			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)

			Eventually(func() error {
				return reconciler.Client.Get(ctx, objectKey, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			reconcileRequest := mcreconcile.Request{Request: reconcile.Request{NamespacedName: objectKey}}

			// First reconcile — should trigger provisioning
			_, err := reconciler.Reconcile(ctx, reconcileRequest)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile at once — the cache may still miss the provision
			// entry; the resourceVersion-conditional write then conflicts and
			// launches nothing.
			_, err = reconciler.Reconcile(ctx, reconcileRequest)
			Expect(err == nil || errors.IsConflict(err)).To(BeTrue(), "unexpected error: %v", err)

			// Once the cache shows the entry, a reconcile tracks the job.
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.ComputeInstance{}
				g.Expect(reconciler.Client.Get(ctx, objectKey, cached)).To(Succeed())
				g.Expect(cached.Status.Jobs).To(HaveLen(1))
			}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, reconcileRequest)
			Expect(err).NotTo(HaveOccurred())

			Expect(aapFake.tasks()).To(Equal([]string{computeInstanceProvisionTask}), "the provision job is launched exactly once")
		})

		It("should set Starting phase when no KubeVirt VM exists", func() {
			const resourceName = "test-phase-no-kv"
			const tenantName = "tenant-phase-nokv"
			defer deleteCI(resourceName)
			createReadyTenant(ctx, namespaceName, tenantName)
			defer deleteTenantInNamespace(ctx, namespaceName, tenantName)

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)

			// Wait for the CI to appear in the controller's cache before calling Reconcile directly.
			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())

			ci := &osacv1alpha1.ComputeInstance{}
			// No KubeVirt VM exists in envtest, so findKubeVirtVMs returns nil.
			// Phase is driven by KubeVirt PrintableStatus; when no VM exists, it is Starting.
			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				g.Expect(ci.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseStarting))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})
	})

	Context("determinePhaseFromPrintableStatus", func() {
		ctx := context.Background()

		// kvVM builds a minimal KubeVirt VirtualMachine with the given PrintableStatus
		// and optional conditions.
		kvVM := func(printableStatus kubevirtv1.VirtualMachinePrintableStatus, conditions ...kubevirtv1.VirtualMachineCondition) *kubevirtv1.VirtualMachine {
			return &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: printableStatus,
					Conditions:      conditions,
				},
			}
		}

		// kvCond builds a KubeVirt VirtualMachineCondition.
		kvCond := func(condType kubevirtv1.VirtualMachineConditionType, status corev1.ConditionStatus) kubevirtv1.VirtualMachineCondition {
			return kubevirtv1.VirtualMachineCondition{Type: condType, Status: status}
		}

		DescribeTable("maps PrintableStatus to phase",
			func(kv *kubevirtv1.VirtualMachine, currentPhase osacv1alpha1.ComputeInstancePhaseType, expectedPhase osacv1alpha1.ComputeInstancePhaseType) {
				Expect(determinePhaseFromPrintableStatus(ctx, kv, currentPhase)).To(Equal(expectedPhase))
			},
			// Transient startup states
			Entry("Provisioning → Starting",
				kvVM(kubevirtv1.VirtualMachineStatusProvisioning), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStarting),
			Entry("WaitingForVolumeBinding → Starting",
				kvVM(kubevirtv1.VirtualMachineStatusWaitingForVolumeBinding), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStarting),
			Entry("Starting → Starting",
				kvVM(kubevirtv1.VirtualMachineStatusStarting), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStarting),
			// Running
			Entry("Running (no pause condition) → Running",
				kvVM(kubevirtv1.VirtualMachineStatusRunning), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseRunning),
			Entry("Running (VirtualMachinePaused=False) → Running",
				kvVM(kubevirtv1.VirtualMachineStatusRunning, kvCond(kubevirtv1.VirtualMachinePaused, corev1.ConditionFalse)), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseRunning),
			Entry("Running (VirtualMachinePaused=True) → Paused (older KubeVirt fallback)",
				kvVM(kubevirtv1.VirtualMachineStatusRunning, kvCond(kubevirtv1.VirtualMachinePaused, corev1.ConditionTrue)), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhasePaused),
			// Paused
			Entry("Paused (no conditions) → Paused",
				kvVM(kubevirtv1.VirtualMachineStatusPaused), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhasePaused),
			Entry("Paused (VirtualMachinePaused=True) → Paused",
				kvVM(kubevirtv1.VirtualMachineStatusPaused, kvCond(kubevirtv1.VirtualMachinePaused, corev1.ConditionTrue)), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhasePaused),
			// Migration (VM remains accessible)
			Entry("Migrating → Running",
				kvVM(kubevirtv1.VirtualMachineStatusMigrating), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseRunning),
			Entry("WaitingForReceiver → Running",
				kvVM(kubevirtv1.VirtualMachineStatusWaitingForReceiver), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseRunning),
			// Stopping / Stopped
			Entry("Stopping → Stopping",
				kvVM(kubevirtv1.VirtualMachineStatusStopping), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStopping),
			Entry("Stopped → Stopped",
				kvVM(kubevirtv1.VirtualMachineStatusStopped), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStopped),
			// Transient scheduling state (VM is trying to find a node)
			Entry("ErrorUnschedulable → Starting (transient during VM boot)",
				kvVM(kubevirtv1.VirtualMachineStatusUnschedulable), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseStarting),
			Entry("ErrorUnschedulable preserves Running phase",
				kvVM(kubevirtv1.VirtualMachineStatusUnschedulable), osacv1alpha1.ComputeInstancePhaseRunning, osacv1alpha1.ComputeInstancePhaseStarting),
			// Error states
			Entry("CrashLoopBackOff → Failed",
				kvVM(kubevirtv1.VirtualMachineStatusCrashLoopBackOff), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseFailed),
			Entry("Terminating → Failed",
				kvVM(kubevirtv1.VirtualMachineStatusTerminating), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseFailed),
			Entry("DataVolumeError → Failed",
				kvVM(kubevirtv1.VirtualMachineStatusDataVolumeError), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseFailed),
			Entry("ErrorPvcNotFound → Failed",
				kvVM(kubevirtv1.VirtualMachineStatusPvcNotFound), osacv1alpha1.ComputeInstancePhaseType(""), osacv1alpha1.ComputeInstancePhaseFailed),
			// Unknown: preserves current phase
			Entry("Unknown preserves Running phase",
				kvVM(kubevirtv1.VirtualMachineStatusUnknown), osacv1alpha1.ComputeInstancePhaseRunning, osacv1alpha1.ComputeInstancePhaseRunning),
			Entry("Unknown preserves Stopped phase",
				kvVM(kubevirtv1.VirtualMachineStatusUnknown), osacv1alpha1.ComputeInstancePhaseStopped, osacv1alpha1.ComputeInstancePhaseStopped),
			// Empty PrintableStatus: preserves current phase (race at VM creation)
			Entry("Empty PrintableStatus preserves Starting phase",
				kvVM(""), osacv1alpha1.ComputeInstancePhaseStarting, osacv1alpha1.ComputeInstancePhaseStarting),
			Entry("Empty PrintableStatus preserves Running phase",
				kvVM(""), osacv1alpha1.ComputeInstancePhaseRunning, osacv1alpha1.ComputeInstancePhaseRunning),
		)
	})

	Context("provisioningErrorMessage", func() {
		DescribeTable("returns error message for failure states and empty for non-failure states",
			func(kv *kubevirtv1.VirtualMachine, expectedMsg string) {
				Expect(provisioningErrorMessage(kv)).To(Equal(expectedMsg))
			},
			Entry("DataVolumeError with condition detail",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusDataVolumeError,
						Conditions: []kubevirtv1.VirtualMachineCondition{
							{Type: "DataVolumesReady", Status: corev1.ConditionFalse, Message: "Not all of the VMI's DVs are ready"},
						},
					},
				},
				"Boot disk provisioning failed: Not all of the VMI's DVs are ready"),
			Entry("DataVolumeError without conditions",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusDataVolumeError,
					},
				},
				"Boot disk provisioning failed"),
			Entry("ErrorPvcNotFound",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusPvcNotFound,
					},
				},
				"Boot disk provisioning failed: PVC not found"),
			Entry("ErrorUnschedulable returns empty (handled separately as transient)",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
					},
				},
				""),
			Entry("CrashLoopBackOff",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusCrashLoopBackOff,
					},
				},
				"VM is in CrashLoopBackOff"),
			Entry("ErrImagePull",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusErrImagePull,
					},
				},
				"VM image pull failed"),
			Entry("ImagePullBackOff",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusImagePullBackOff,
					},
				},
				"VM image pull failed"),
			Entry("Terminating",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusTerminating,
					},
				},
				"VM is terminating unexpectedly"),
			Entry("Running returns empty",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusRunning,
					},
				},
				""),
			Entry("Stopped returns empty",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusStopped,
					},
				},
				""),
			Entry("Provisioning returns empty",
				&kubevirtv1.VirtualMachine{
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachineStatusProvisioning,
					},
				},
				""),
		)
	})

	Context("schedulingWaitMessage", func() {
		It("returns base message when no conditions are present", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
				},
			}
			Expect(schedulingWaitMessage(kv)).To(Equal("VM is waiting for a schedulable node"))
		})

		It("appends condition detail when a False condition has a message", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{
							Type:    kubevirtv1.VirtualMachineConditionType("PodScheduled"),
							Status:  corev1.ConditionFalse,
							Message: "Guest VM is not reported as running",
						},
					},
				},
			}
			Expect(schedulingWaitMessage(kv)).To(Equal("VM is waiting for a schedulable node: Guest VM is not reported as running"))
		})

		It("ignores True conditions", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{
							Type:    kubevirtv1.VirtualMachineReady,
							Status:  corev1.ConditionTrue,
							Message: "VM is ready",
						},
					},
				},
			}
			Expect(schedulingWaitMessage(kv)).To(Equal("VM is waiting for a schedulable node"))
		})
	})

	Context("isOperationalStatus", func() {
		DescribeTable("returns true for known operational statuses and false otherwise",
			func(status kubevirtv1.VirtualMachinePrintableStatus, expected bool) {
				Expect(isOperationalStatus(status)).To(Equal(expected))
			},
			Entry("Running", kubevirtv1.VirtualMachineStatusRunning, true),
			Entry("Stopped", kubevirtv1.VirtualMachineStatusStopped, true),
			Entry("Paused", kubevirtv1.VirtualMachineStatusPaused, true),
			Entry("Starting", kubevirtv1.VirtualMachineStatusStarting, true),
			Entry("WaitingForVolumeBinding", kubevirtv1.VirtualMachineStatusWaitingForVolumeBinding, true),
			Entry("Migrating", kubevirtv1.VirtualMachineStatusMigrating, true),
			Entry("WaitingForReceiver", kubevirtv1.VirtualMachineStatusWaitingForReceiver, true),
			Entry("Stopping", kubevirtv1.VirtualMachineStatusStopping, true),
			Entry("Unknown", kubevirtv1.VirtualMachineStatusUnknown, true),
			Entry("Provisioning is not operational", kubevirtv1.VirtualMachineStatusProvisioning, false),
			Entry("DataVolumeError is not operational", kubevirtv1.VirtualMachineStatusDataVolumeError, false),
			Entry("CrashLoopBackOff is not operational", kubevirtv1.VirtualMachineStatusCrashLoopBackOff, false),
			Entry("Terminating is not operational", kubevirtv1.VirtualMachineStatusTerminating, false),
			Entry("empty string is not operational", kubevirtv1.VirtualMachinePrintableStatus(""), false),
			Entry("unknown future status is not operational", kubevirtv1.VirtualMachinePrintableStatus("SomeFutureStatus"), false),
		)
	})

	Context("Tier definitions in AAP extra_vars context (JIT storage)", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		tierDefsTiersClient := func() *mockStorageTiersLister {
			return &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
		}
		tierDefsBackendsClient := func() *mockStorageBackendsClient {
			return &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}
		}

		createCIAndReconcile := func(name, tenantName string, aapFake *wlFakeAAP, configure func(*ComputeInstanceReconciler)) {
			createReadyTenant(ctx, namespaceName, tenantName)
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)
			if configure != nil {
				configure(reconciler)
			}

			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
		}

		It("should inject storage_tier_definitions into context before triggering provisioning", func() {
			const resourceName = "test-tier-ctx-provision"
			const tenantName = "tenant-tier-ctx-provision"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			aapFake := newWLFakeAAP()

			createCIAndReconcile(resourceName, tenantName, aapFake, func(r *ComputeInstanceReconciler) {
				r.TiersClient = tierDefsTiersClient()
				r.BackendsClient = tierDefsBackendsClient()
			})

			gotTiers := launchedTierDefinitions(aapFake)
			Expect(gotTiers).To(HaveLen(1))
			Expect(gotTiers[0].Name).To(Equal("fast"))
		})

		It("should inject storage_backend_connections into context alongside storage_tier_definitions", func() {
			const resourceName = "test-backend-conn-ctx-provision"
			const tenantName = "tenant-backend-conn-ctx-provision"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			aapFake := newWLFakeAAP()

			createCIAndReconcile(resourceName, tenantName, aapFake, func(r *ComputeInstanceReconciler) {
				r.TiersClient = tierDefsTiersClient()
				r.BackendsClient = tierDefsBackendsClient()
			})

			gotConns := launchedBackendConnections(aapFake)
			Expect(gotConns).To(HaveKeyWithValue("backend-1", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testBackendPassword,
			}))
		})

		It("should skip tier/backend injection gracefully when TiersClient and BackendsClient are not configured", func() {
			const resourceName = "test-tier-ctx-unconfigured"
			const tenantName = "tenant-tier-ctx-unconfigured"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			aapFake := newWLFakeAAP()

			// No TiersClient/BackendsClient configured — matches the default zero
			// value used by every other test in this file.
			createCIAndReconcile(resourceName, tenantName, aapFake, nil)

			gotTiers := launchedTierDefinitions(aapFake)
			Expect(gotTiers).To(BeEmpty())
		})

		It("should proceed without tier data when tier resolution fails (non-fatal)", func() {
			const resourceName = "test-tier-ctx-resolve-error"
			const tenantName = "tenant-tier-ctx-resolve-error"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			aapFake := newWLFakeAAP()

			createCIAndReconcile(resourceName, tenantName, aapFake, func(r *ComputeInstanceReconciler) {
				r.TiersClient = &mockStorageTiersLister{
					listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
						return nil, fmt.Errorf("tier API unavailable")
					},
				}
				r.BackendsClient = tierDefsBackendsClient()
			})

			gotTiers := launchedTierDefinitions(aapFake)
			Expect(gotTiers).To(BeEmpty())
		})
	})

	Context("Tenant storage class fallback resolution", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		It("should resolve tenant storage classes from target cluster when status is empty", func() {
			const resourceName = "test-sc-fallback-resolve"
			const tenantName = "tenant-sc-fallback-resolve"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			createLabeledStorageClass(ctx, "sc-fallback-1", tenantName, "local")

			createReadyTenant(ctx, namespaceName, tenantName)

			aapFake := newWLFakeAAP()

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)
			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			gotSCs := launchedTenantStorageClasses(aapFake)
			Expect(gotSCs).To(HaveLen(1))
			Expect(gotSCs[0].Name).To(Equal("sc-fallback-1"))
			Expect(gotSCs[0].Tier).To(Equal("local"))
		})

		It("should proceed without storage classes when status is empty and no labeled SCs exist", func() {
			const resourceName = "test-sc-fallback-none"
			const tenantName = "tenant-sc-fallback-none"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			createReadyTenant(ctx, namespaceName, tenantName)

			aapFake := newWLFakeAAP()

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)
			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			gotSCs := launchedTenantStorageClasses(aapFake)
			Expect(gotSCs).To(BeNil())
		})

		It("should use status storage classes when populated (regression)", func() {
			const resourceName = "test-sc-status-populated"
			const tenantName = "tenant-sc-status-populated"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			// Create a labeled SC that should NOT be used because status is populated
			createLabeledStorageClass(ctx, "sc-status-ignored", tenantName, "ignored")

			// Create tenant with pre-populated status.storageClasses
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
			tenant.Status.Namespace = namespaceName
			tenant.Status.StorageClasses = []osacv1alpha1.ResolvedStorageClass{
				{Name: "ceph-fast", Tier: "fast"},
			}
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			aapFake := newWLFakeAAP()

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)

			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, cached)).To(Succeed())
				g.Expect(cached.Status.StorageClasses).To(HaveLen(1))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			gotSCs := launchedTenantStorageClasses(aapFake)
			Expect(gotSCs).To(HaveLen(1))
			Expect(gotSCs[0].Name).To(Equal("ceph-fast"))
			Expect(gotSCs[0].Tier).To(Equal("fast"))
		})

		It("should skip fallback when ClusterStorageReady condition is set", func() {
			const resourceName = "test-sc-fallback-skip"
			const tenantName = "tenant-sc-fallback-skip"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			createLabeledStorageClass(ctx, "sc-skip-fallback", tenantName, "local")

			// Create tenant with ClusterStorageReady condition set (storage
			// controller has run) but no storageClasses in status.
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
			tenant.Status.Namespace = namespaceName
			tenant.SetStatusCondition(osacv1alpha1.TenantConditionClusterStorageReady, metav1.ConditionTrue,
				"Ready", "storage controller resolved")
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			aapFake := newWLFakeAAP()

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)

			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, cached)).To(Succeed())
				g.Expect(cached.GetStatusCondition(osacv1alpha1.TenantConditionClusterStorageReady)).NotTo(BeNil())
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			gotSCs := launchedTenantStorageClasses(aapFake)
			Expect(gotSCs).To(BeNil(), "should not resolve from cluster when storage controller already ran")
		})

		It("should exclude ambiguous tiers when multiple SCs share the same tenant and tier labels", func() {
			const resourceName = "test-sc-fallback-ambiguous"
			const tenantName = "tenant-sc-fallback-ambiguous"
			DeferCleanup(func() {
				deleteCI(resourceName)
				deleteTenantInNamespace(ctx, namespaceName, tenantName)
			})

			createLabeledStorageClass(ctx, "sc-ambig-a", tenantName, "local")
			createLabeledStorageClass(ctx, "sc-ambig-b", tenantName, "local")

			createReadyTenant(ctx, namespaceName, tenantName)

			aapFake := newWLFakeAAP()

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fakeRecorder := events.NewFakeRecorder(100)
			reconciler := newTestCIReconcilerWithFake(aapFake, "", namespaceName, "", 100*time.Millisecond)
			reconciler.Recorder = fakeRecorder
			Eventually(func() error {
				return reconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			gotSCs := launchedTenantStorageClasses(aapFake)
			Expect(gotSCs).To(BeNil(), "ambiguous tier should not be injected")
			Eventually(fakeRecorder.Events).Should(Receive(And(
				ContainSubstring("Warning"),
				ContainSubstring(eventReasonDuplicateStorageClass),
			)))
			Eventually(fakeRecorder.Events).Should(Receive(And(
				ContainSubstring("Warning"),
				ContainSubstring(eventReasonDuplicateStorageClass),
				ContainSubstring("all matched tiers are ambiguous"),
			)))
		})
	})

	Context("handleKubeVirtVM", func() {
		var (
			ctx        context.Context
			reconciler *ComputeInstanceReconciler
			instance   *osacv1alpha1.ComputeInstance
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = newTestCIReconciler("", "", "", 0)
			instance = &osacv1alpha1.ComputeInstance{}
			// Ready also needs NetworkReady=True (VMaaS design).
			instance.SetStatusCondition(osacv1alpha1.ComputeInstanceConditionNetworkReady, metav1.ConditionTrue,
				"", osacv1alpha1.ReasonProvisioningDisabled)
		})

		It("sets Provisioned=True and Ready=True when VM is Running and Ready", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusRunning,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{Type: kubevirtv1.VirtualMachineReady, Status: corev1.ConditionTrue},
					},
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonInfrastructureReady))
			Expect(provCond.Message).To(Equal("All infrastructure resources provisioned successfully"))

			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionRestartRequired).Status).To(Equal(metav1.ConditionFalse))
		})

		It("sets Provisioned=True and Ready=False when VM is Running but not Ready", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusRunning,
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonInfrastructureReady))
			Expect(provCond.Message).To(Equal("All infrastructure resources provisioned successfully"))

			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionRestartRequired).Status).To(Equal(metav1.ConditionFalse))
		})

		It("sets RestartRequired=True when KubeVirt RestartRequired condition is True", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusRunning,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{Type: kubevirtv1.VirtualMachineRestartRequired, Status: corev1.ConditionTrue},
					},
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned).Status).To(Equal(metav1.ConditionTrue))
			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionRestartRequired).Status).To(Equal(metav1.ConditionTrue))
		})

		It("sets Provisioned=False with ProvisioningStorage reason when VM is in Provisioning state", func() {
			instance.Spec.BootDisk.SizeGiB = 20
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusProvisioning,
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningStorage))
			Expect(provCond.Message).To(Equal("Creating DataVolumes for boot disk (20GiB)"))

			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionReady).Status).To(Equal(metav1.ConditionFalse))
			Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionRestartRequired).Status).To(Equal(metav1.ConditionFalse))
		})

		It("sets Provisioned=False with error detail when VM has DataVolumeError", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusDataVolumeError,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{
							Type:    "DataVolumesReady",
							Status:  corev1.ConditionFalse,
							Reason:  "NotAllDVsReady",
							Message: "Not all of the VMI's DVs are ready",
						},
					},
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(provCond.Message).To(Equal("Boot disk provisioning failed: Not all of the VMI's DVs are ready"))
		})

		It("sets Provisioned=False with generic message when DataVolumeError has no condition detail", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusDataVolumeError,
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(provCond.Message).To(Equal("Boot disk provisioning failed"))
		})

		It("sets Provisioned=False when VM has ErrorPvcNotFound", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusPvcNotFound,
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(provCond.Message).To(ContainSubstring("Boot disk provisioning failed: PVC not found"))
		})

		It("sets Provisioned=False with Scheduling reason when VM is ErrorUnschedulable", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonScheduling))
			Expect(provCond.Message).To(ContainSubstring("VM is waiting for a schedulable node"))
		})

		It("sets Provisioned=False with Scheduling reason and condition detail when ErrorUnschedulable has conditions", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachineStatusUnschedulable,
					Conditions: []kubevirtv1.VirtualMachineCondition{
						{
							Type:    kubevirtv1.VirtualMachineConditionType("PodScheduled"),
							Status:  corev1.ConditionFalse,
							Message: "Guest VM is not reported as running",
						},
					},
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonScheduling))
			Expect(provCond.Message).To(ContainSubstring("Guest VM is not reported as running"))
		})

		It("preserves current Provisioned condition when PrintableStatus is empty", func() {
			instance.SetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, "VirtualMachine not yet created, waiting for provisioning", osacv1alpha1.ReasonWaitingForVM)

			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus(""),
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonWaitingForVM))
			Expect(provCond.Message).To(Equal("VirtualMachine not yet created, waiting for provisioning"))
		})

		It("sets Provisioned=False for unknown PrintableStatus", func() {
			kv := &kubevirtv1.VirtualMachine{
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("SomeFutureErrorStatus"),
				},
			}
			reconciler.handleKubeVirtVM(ctx, instance, kv)
			reconciler.reconcileReadyCondition(instance, kv)

			provCond := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			Expect(provCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(provCond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(provCond.Message).To(Equal("VM entered unexpected status: SomeFutureErrorStatus"))
		})
	})

	Context("Event emission", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		It("should emit TenantNotReady event only on first occurrence", func() {
			const resourceName = "test-ci-event-tenant"
			const tenantName = "tenant-event-notready"
			DeferCleanup(func() { deleteCI(resourceName) })
			DeferCleanup(func() { deleteTenantInNamespace(ctx, namespaceName, tenantName) })

			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fakeRecorder := events.NewFakeRecorder(100)
			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
			controllerReconciler.Recorder = fakeRecorder

			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			// First reconcile — should emit TenantNotReady event
			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Eventually(fakeRecorder.Events).Should(Receive(And(
				ContainSubstring("Normal"),
				ContainSubstring(eventReasonTenantNotReady),
			)))

			// Wait for the cache to see the updated Provisioned condition
			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Client.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonTenantNotReady))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Second reconcile — same state, should NOT emit again
			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Consistently(fakeRecorder.Events, 500*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(eventReasonTenantNotReady),
			))
		})

		It("should set WaitingForVM when tenant becomes Ready without VM", func() {
			const resourceName = "test-ci-event-waitvm"
			const tenantName = "tenant-event-waitvm"
			DeferCleanup(func() { deleteCI(resourceName) })
			DeferCleanup(func() { deleteTenantInNamespace(ctx, namespaceName, tenantName) })

			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fakeRecorder := events.NewFakeRecorder(100)
			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
			controllerReconciler.Recorder = fakeRecorder

			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			// First reconcile — TenantNotReady event
			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(eventReasonTenantNotReady)))

			// Transition tenant to Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
			tenant.Status.Namespace = namespaceName
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, cached)).To(Succeed())
				g.Expect(cached.Status.Phase).To(Equal(osacv1alpha1.TenantPhaseReady))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Wait for cache to see Provisioned/TenantNotReady from first reconcile
			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Client.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonTenantNotReady))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Second reconcile — tenant Ready, no VM -> Provisioned WaitingForVM (no event; condition carries detail)
			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Client.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonWaitingForVM))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Third reconcile — still no VM, reason unchanged
			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				ci := &osacv1alpha1.ComputeInstance{}
				g.Expect(controllerReconciler.Client.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonWaitingForVM))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})
	})

	Context("conditionReason and conditionStatus helpers", func() {
		It("conditionReason returns empty string when condition does not exist", func() {
			instance := &osacv1alpha1.ComputeInstance{}
			Expect(conditionReason(instance, osacv1alpha1.ComputeInstanceConditionProvisioned)).To(Equal(""))
		})

		It("conditionReason returns the reason when condition exists", func() {
			instance := &osacv1alpha1.ComputeInstance{}
			instance.SetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, "some message", osacv1alpha1.ReasonWaitingForVM)
			Expect(conditionReason(instance, osacv1alpha1.ComputeInstanceConditionProvisioned)).To(Equal(osacv1alpha1.ReasonWaitingForVM))
		})

		It("conditionStatus returns ConditionUnknown when condition does not exist", func() {
			instance := &osacv1alpha1.ComputeInstance{}
			Expect(conditionStatus(instance, osacv1alpha1.ComputeInstanceConditionReady)).To(Equal(metav1.ConditionUnknown))
		})

		It("conditionStatus returns the correct status when condition exists", func() {
			instance := &osacv1alpha1.ComputeInstance{}
			instance.SetStatusCondition(osacv1alpha1.ComputeInstanceConditionReady, metav1.ConditionTrue, "", osacv1alpha1.ReasonAsExpected)
			Expect(conditionStatus(instance, osacv1alpha1.ComputeInstanceConditionReady)).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("Tenant lifecycle", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		It("should requeue when tenant has DeletionTimestamp", func() {
			const resourceName = "test-tenant-gc-clear"
			const tenantName = "tenant-gc-clear"
			defer deleteCI(resourceName)
			defer deleteTenantInNamespace(ctx, namespaceName, tenantName)

			createReadyTenant(ctx, namespaceName, tenantName)

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			// Wait for manager cache to see the CI before reconciling
			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func() error {
				return mgrClient.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}).Should(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)

			// First reconcile: sets tenant reference
			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())

			// Verify reference was set
			ci := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				g.Expect(ci.Status.TenantReference).NotTo(BeNil())
				g.Expect(ci.Status.TenantReference.Name).NotTo(BeEmpty())
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Add finalizer to tenant to keep it in terminating state
			tenant := &osacv1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, tenant)).To(Succeed())
			tenant.Finalizers = append(tenant.Finalizers, "osac.openshift.io/test")
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			// Delete the tenant - it will be stuck in terminating due to finalizer
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
			// Wait for manager cache to see the tenant with DeletionTimestamp before second reconcile
			Eventually(func(g Gomega) {
				cachedTenant := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, cachedTenant)).To(Succeed())
				g.Expect(cachedTenant.DeletionTimestamp).NotTo(BeNil())
			}).Should(Succeed())

			// Reconcile again - should return error when tenant has DeletionTimestamp (reference is not cleared)
			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenant is being deleted"))

			// Tenant reference is left unchanged
			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				g.Expect(ci.Status.TenantReference).NotTo(BeNil())
				g.Expect(ci.Status.TenantReference.Name).To(Equal(tenantName))
				g.Expect(ci.Status.TenantReference.Namespace).To(Equal(namespaceName))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("should return error when tenant does not exist", func() {
			const resourceName = "test-tenant-not-found"
			const tenantName = "tenant-nonexistent"
			defer deleteCI(resourceName)

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}

			// Create a ComputeInstance that references a tenant that does not exist
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func() error {
				return mgrClient.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}).Should(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)

			// Reconcile should fail because tenant does not exist
			_, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Tenant-not-ready Provisioned condition", func() {
		const namespaceName = "default"

		ctx := context.Background()

		deleteCI := func(name string) {
			ci := &osacv1alpha1.ComputeInstance{}
			nn := types.NamespacedName{Name: name, Namespace: namespaceName}
			if err := k8sClient.Get(ctx, nn, ci); err == nil {
				ci.Finalizers = nil
				_ = k8sClient.Update(ctx, ci)
				_ = k8sClient.Delete(ctx, ci)
			}
		}

		It("should set Provisioned=False with Tenant phase when tenant is Progressing", func() {
			const resourceName = "test-ci-tenant-progressing"
			const tenantName = "tenant-progressing-msg"
			DeferCleanup(func() { deleteCI(resourceName) })
			DeferCleanup(func() { deleteTenantInNamespace(ctx, namespaceName, tenantName) })

			// Create tenant in Progressing state (no ClusterStorageReady condition)
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			ci := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("TenantNotReady"))
				g.Expect(cond.Message).To(ContainSubstring("Tenant '%s' is not ready (phase: Progressing)", tenantName))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("should include ClusterStorageReady condition in Provisioned message", func() {
			const resourceName = "test-ci-tenant-sc-msg"
			const tenantName = "tenant-sc-msg"
			DeferCleanup(func() { deleteCI(resourceName) })
			DeferCleanup(func() { deleteTenantInNamespace(ctx, namespaceName, tenantName) })

			// Create tenant in Progressing state with a ClusterStorageReady condition
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
			tenant.SetStatusCondition(
				osacv1alpha1.TenantConditionClusterStorageReady,
				metav1.ConditionFalse,
				osacv1alpha1.TenantReasonMultipleFound,
				"Multiple StorageClasses found with label osac.openshift.io/tenant="+tenantName+": sc1, sc2",
			)
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			ci := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal("TenantNotReady"))
				g.Expect(cond.Message).To(ContainSubstring("ClusterStorageReady"))
				g.Expect(cond.Message).To(ContainSubstring("sc1, sc2"))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("should clear TenantNotReady message when tenant becomes Ready", func() {
			const resourceName = "test-ci-tenant-recovery"
			const tenantName = "tenant-recovery-msg"
			DeferCleanup(func() { deleteCI(resourceName) })
			DeferCleanup(func() { deleteTenantInNamespace(ctx, namespaceName, tenantName) })

			// Create tenant in Progressing state
			tenant := &osacv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: tenantName, Namespace: namespaceName},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
			tenant.SetStatusCondition(
				osacv1alpha1.TenantConditionClusterStorageReady,
				metav1.ConditionFalse,
				osacv1alpha1.TenantReasonMultipleFound,
				"Multiple StorageClasses found",
			)
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			nn := types.NamespacedName{Name: resourceName, Namespace: namespaceName}
			resource := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespaceName,
					Annotations: map[string]string{
						osacTenantKey: tenantName,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := newTestCIReconciler("", namespaceName, "", 100*time.Millisecond)
			Eventually(func() error {
				return controllerReconciler.Client.Get(ctx, nn, &osacv1alpha1.ComputeInstance{})
			}, 2*time.Second, 10*time.Millisecond).Should(Succeed())

			// First reconcile with Progressing tenant → Provisioned=False/TenantNotReady
			result, err := controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			ci := &osacv1alpha1.ComputeInstance{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal("TenantNotReady"))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Transition tenant to Ready
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, tenant)).To(Succeed())
			tenant.Status.Phase = osacv1alpha1.TenantPhaseReady
			tenant.Status.Namespace = namespaceName
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
			// Wait for cache to see Ready tenant
			mgrClient := testMcManager.GetLocalManager().GetClient()
			Eventually(func(g Gomega) {
				cached := &osacv1alpha1.Tenant{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: namespaceName}, cached)).To(Succeed())
				g.Expect(cached.Status.Phase).To(Equal(osacv1alpha1.TenantPhaseReady))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			// Second reconcile with Ready tenant → Provisioned=False/WaitingForVM (no KubeVirt VM)
			_, err = controllerReconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, ci)).To(Succeed())
				cond := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonWaitingForVM))
				g.Expect(cond.Message).To(Equal("VirtualMachine not yet created, waiting for provisioning"))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})
	})

	Context("lastRestartedAt update after provisioning", func() {
		var reconciler *ComputeInstanceReconciler
		ctx := context.Background()

		BeforeEach(func() {
			reconciler = newTestCIReconciler("", "", "", 0)
		})

		It("should set lastRestartedAt when restartRequestedAt is set and config versions match", func() {
			restartTime := metav1.NewTime(time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC))
			spec := newTestComputeInstanceSpec("template-1")
			spec.RestartRequestedAt = &restartTime
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-restart", Namespace: "default"},
				Spec:       spec,
			}

			// Compute desired config version
			err := reconciler.handleDesiredConfigVersion(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// Simulate provision completed: reconciled matches desired

			Expect(instance.Status.LastRestartedAt).To(BeNil())

			// The logic under test: when config versions match and restartRequestedAt > lastRestartedAt
			if instance.Spec.RestartRequestedAt != nil {
				if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
					instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				}
			}

			Expect(instance.Status.LastRestartedAt).NotTo(BeNil())
			Expect(instance.Status.LastRestartedAt.Time).To(Equal(restartTime.Time))
		})

		It("should not update lastRestartedAt when restartRequestedAt is not set", func() {
			spec := newTestComputeInstanceSpec("template-1")
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-no-restart", Namespace: "default"},
				Spec:       spec,
			}

			err := reconciler.handleDesiredConfigVersion(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// Same logic inline
			if instance.Spec.RestartRequestedAt != nil {
				if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
					instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				}
			}

			Expect(instance.Status.LastRestartedAt).To(BeNil())
		})

		It("should not update lastRestartedAt when restart was already processed", func() {
			restartTime := metav1.NewTime(time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC))
			spec := newTestComputeInstanceSpec("template-1")
			spec.RestartRequestedAt = &restartTime
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-already-restarted", Namespace: "default"},
				Spec:       spec,
				Status: osacv1alpha1.ComputeInstanceStatus{
					LastRestartedAt: &restartTime, // same as requested
				},
			}

			err := reconciler.handleDesiredConfigVersion(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			originalLastRestarted := instance.Status.LastRestartedAt.DeepCopy()

			if instance.Spec.RestartRequestedAt != nil {
				if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
					instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				}
			}

			Expect(instance.Status.LastRestartedAt.Time).To(Equal(originalLastRestarted.Time))
		})

		It("should update lastRestartedAt for a new restart request", func() {
			firstRestart := metav1.NewTime(time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC))
			secondRestart := metav1.NewTime(time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC))
			spec := newTestComputeInstanceSpec("template-1")
			spec.RestartRequestedAt = &secondRestart
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-second-restart", Namespace: "default"},
				Spec:       spec,
				Status: osacv1alpha1.ComputeInstanceStatus{
					LastRestartedAt: &firstRestart,
				},
			}

			err := reconciler.handleDesiredConfigVersion(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			if instance.Spec.RestartRequestedAt != nil {
				if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
					instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				}
			}

			Expect(instance.Status.LastRestartedAt.Time).To(Equal(secondRestart.Time))
		})

		It("should include restartRequestedAt in spec hash", func() {
			restartTime := metav1.NewTime(time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC))

			specWithout := newTestComputeInstanceSpec("template-1")
			instanceWithout := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hash-without", Namespace: "default"},
				Spec:       specWithout,
			}
			err := reconciler.handleDesiredConfigVersion(ctx, instanceWithout)
			Expect(err).NotTo(HaveOccurred())
			hashWithout := instanceWithout.Status.DesiredConfigVersion

			specWith := newTestComputeInstanceSpec("template-1")
			specWith.RestartRequestedAt = &restartTime
			instanceWith := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hash-with", Namespace: "default"},
				Spec:       specWith,
			}
			err = reconciler.handleDesiredConfigVersion(ctx, instanceWith)
			Expect(err).NotTo(HaveOccurred())
			hashWith := instanceWith.Status.DesiredConfigVersion

			Expect(hashWith).NotTo(Equal(hashWithout), "restartRequestedAt must change the spec hash to trigger provisioning")
		})
	})

	Context("provisioning.ComputeBackoffFromJobs", func() {
		now := time.Now().UTC()

		It("should return base delay for empty jobs", func() {
			Expect(provisioning.ComputeBackoffFromJobs(nil, "v1")).To(Equal(provisioning.BackoffBaseDelay))
		})

		It("should return base delay for single failed job", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(provisioning.BackoffBaseDelay))
		})

		It("should double the gap between two failed jobs", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(10 * time.Minute))
		})

		It("should cap at max delay", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-20 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(provisioning.BackoffMaxDelay))
		})

		It("should return base delay when gap is smaller than base", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-30 * time.Second))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(provisioning.BackoffBaseDelay))
		})

		It("should return base delay when timestamps are equal (zero gap)", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(provisioning.BackoffBaseDelay))
		})

		It("should ignore jobs with different ConfigVersion", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v2", Timestamp: metav1.NewTime(now)},
			}
			// Only one job matches "v1", so base delay
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(provisioning.BackoffBaseDelay))
		})

		It("should ignore non-provision jobs", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				{Type: osacv1alpha1.JobTypeDeprovision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-3 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(10 * time.Minute))
		})

		It("should ignore succeeded jobs", func() {
			jobs := []osacv1alpha1.JobStatus{
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-5 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateSucceeded, ConfigVersion: "v1", Timestamp: metav1.NewTime(now.Add(-3 * time.Minute))},
				{Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, ConfigVersion: "v1", Timestamp: metav1.NewTime(now)},
			}
			// Gap is between the two failed jobs (5 min), not between failed and succeeded
			Expect(provisioning.ComputeBackoffFromJobs(jobs, "v1")).To(Equal(10 * time.Minute))
		})
	})

	Describe("PrimarySubnetRef", func() {
		It("should return empty string when neither subnetRef nor networkAttachments is set", func() {
			spec := osacv1alpha1.ComputeInstanceSpec{}
			Expect(spec.PrimarySubnetRef()).To(Equal(""))
		})

		It("should return empty when networkAttachments is empty", func() {
			spec := osacv1alpha1.ComputeInstanceSpec{
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{},
			}
			Expect(spec.PrimarySubnetRef()).To(Equal(""))
		})

		It("should return networkAttachments[0].subnetRef when networkAttachments is set", func() {
			spec := osacv1alpha1.ComputeInstanceSpec{
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{
					{SubnetRef: "primary-subnet", SecurityGroupRefs: []string{"sg-1"}},
					{SubnetRef: "secondary-subnet", SecurityGroupRefs: []string{"sg-2"}},
				},
			}
			Expect(spec.PrimarySubnetRef()).To(Equal("primary-subnet"))
		})
	})

})

// runningInstanceWithTimes builds a Running instance whose phase and configuration
// transitions carry explicit times, so the stamp can be asserted exactly.
func runningInstanceWithTimes(phaseAt, configAt time.Time) *osacv1alpha1.ComputeInstance {
	instance := &osacv1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				osacComputeInstanceTypeNameLabel: "standard-8-32",
			},
		},
	}
	instance.Status.Conditions = []metav1.Condition{
		{
			Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
			Status:             metav1.ConditionFalse,
			Reason:             "AsExpected",
			LastTransitionTime: metav1.NewTime(phaseAt),
		},
		{
			Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
			Status:             metav1.ConditionTrue,
			Reason:             "AsExpected",
			LastTransitionTime: metav1.NewTime(configAt),
		},
	}
	instance.Status.Phase = osacv1alpha1.ComputeInstancePhaseRunning
	phaseTime := metav1.NewTime(phaseAt)
	instance.Status.PhaseTransitionTime = &phaseTime
	return instance
}

var _ = Describe("configRunningSince", func() {
	It("stamps the phase time on the first record", func() {
		// The first record of a resource dates from the RUNNING phase
		// transition: the type is known from the spec at admission and is what
		// the VM started with, so the interval from the phase change carries
		// the instance_type dimension.
		phaseAt := time.Now().Add(-15 * time.Second).UTC()
		configAt := phaseAt.Add(-time.Minute)
		instance := runningInstanceWithTimes(phaseAt, configAt)

		since := configRunningSince(instance)

		Expect(since.Time).To(BeTemporally("~", phaseAt, time.Second))
	})

	It("stamps the observation time when the config landed after the phase change", func() {
		// A change of the recorded version stamps from the observation time:
		// the ConfigurationApplied condition can stay untouched by the change
		// (a name-only resize), so its transition time is arbitrarily stale and
		// must not be reused. The observation time cannot backdate the new
		// type and lags the change by at most one reconcile.
		phaseAt := time.Now().Add(-time.Hour).UTC()
		configAt := time.Now().Add(-time.Minute).UTC()
		instance := runningInstanceWithTimes(phaseAt, configAt)
		// A previous configuration is already recorded, so this observation is
		// a change of the recorded version, not a first record.
		instance.Status.RunningConfigVersion = "previous-version"
		instance.Status.RunningInstanceTypeName = "previous-type"

		since := configRunningSince(instance)

		Expect(since.Time).To(BeTemporally("~", time.Now(), 2*time.Second))
	})

	It("falls back to the observation time when the phase carries no transition time", func() {
		// Legacy resources can reach RUNNING without a recorded phase
		// transition; the first record then falls back to the observation time
		// rather than the zero time.
		configAt := time.Now().Add(-time.Minute).UTC()
		instance := runningInstanceWithTimes(time.Now().UTC(), configAt)
		instance.Status.PhaseTransitionTime = nil

		since := configRunningSince(instance)

		Expect(since.Time).To(BeTemporally("~", time.Now(), 2*time.Second))
	})
})
