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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/profile"
)

// mockClient wraps a real client and allows injecting errors
type mockClient struct {
	client.Client
	createFunc       func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	updateFunc       func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
	deleteFunc       func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	statusUpdateFunc func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error
}

func (m *mockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, obj, opts...)
	}
	return m.Client.Create(ctx, obj, opts...)
}

func (m *mockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, obj, opts...)
	}
	return m.Client.Update(ctx, obj, opts...)
}

func (m *mockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, obj, opts...)
	}
	return m.Client.Delete(ctx, obj, opts...)
}

func (m *mockClient) Status() client.SubResourceWriter {
	return &mockStatusWriter{
		SubResourceWriter: m.Client.Status(),
		updateFunc:        m.statusUpdateFunc,
	}
}

type mockStatusWriter struct {
	client.SubResourceWriter
	updateFunc func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error
}

func (m *mockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, obj, opts...)
	}
	return m.SubResourceWriter.Update(ctx, obj, opts...)
}

// getCondition returns the condition with the given type, or nil if not found
func getCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

var _ = Describe("BareMetalPool Controller", func() {
	var (
		reconciler       *BareMetalPoolReconciler
		mockK8sClient    *mockClient
		mockProvProvider *mockProvisioningProvider
		testPool         *osacv1alpha1.BareMetalPool
		testNamespace    string
		testPoolName     string
	)

	// Common setup for ALL tests
	BeforeEach(func() {
		testNamespace = "default"
		mockK8sClient = &mockClient{Client: k8sClient}
		mockProvProvider = &mockProvisioningProvider{}
		hostReadyPollIntervalDuration := DefaultHostReadyPollIntervalDuration
		hostDeletionPollIntervalDuration := DefaultHostDeletionPollIntervalDuration
		provisionJobPollIntervalDuration := DefaultAAPStatusPollIntervalDuration
		maxJobHistory := DefaultMaxJobHistory

		reconciler = NewBareMetalPoolReconciler(
			mockK8sClient,
			k8sClient.Scheme(),
			mockProvProvider,
			hostReadyPollIntervalDuration,
			hostDeletionPollIntervalDuration,
			provisionJobPollIntervalDuration,
			maxJobHistory,
		)
	})

	// Common cleanup for ALL tests
	AfterEach(func() {
		// Reset all mock functions
		mockK8sClient.createFunc = nil
		mockK8sClient.updateFunc = nil
		mockK8sClient.deleteFunc = nil
		mockK8sClient.statusUpdateFunc = nil

		if testPoolName != "" && testNamespace != "" {
			pool := &osacv1alpha1.BareMetalPool{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, pool)
			if err == nil {
				// Remove finalizer and delete
				pool.Finalizers = []string{}
				_ = k8sClient.Update(ctx, pool)
				_ = k8sClient.Delete(ctx, pool)
			}
		}
	})

	Context("When reconciling a completely new BareMetalPool without finalizer", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-new"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 2,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
		})

		It("should add finalizer on first reconciliation", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			Expect(updatedPool.Finalizers).To(ContainElement(BareMetalPoolFinalizer))
		})

		It("should handle finalizer update error", func() {
			mockK8sClient.updateFunc = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return errors.New("update failed")
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("update failed"))
		})
	})

	Context("When reconciling a BareMetalPool with finalizer", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-with-finalizer"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 3,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
		})

		It("should create BareMetalInstance CRs for the specified replicas", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(3))
		})

		It("should set Ready condition to True", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify pool is NOT ready yet (BareMetalInstances don't have ProvisionTemplateComplete)
			condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			if condition != nil {
				Expect(condition.Status).NotTo(Equal(metav1.ConditionTrue))
				Expect(condition.Reason).NotTo(Equal(osacv1alpha1.BareMetalPoolReasonReady))
			}

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			for i := range bareMetalInstanceList.Items {
				bareMetalInstanceList.Items[i].Status.Phase = osacv1alpha1.BareMetalInstancePhaseReady
				Expect(k8sClient.Status().Update(ctx, &bareMetalInstanceList.Items[i])).To(Succeed())
			}

			// Reconcile again to check readiness
			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			condition = updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonReady))
		})

		It("should verify BareMetalInstance CRs have correct labels and owner references", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())

			for _, bareMetalInstance := range bareMetalInstanceList.Items {
				Expect(bareMetalInstance.Labels[BareMetalPoolLabelKey]).To(Equal(string(updatedPool.UID)))
				Expect(bareMetalInstance.Labels[HostTypeLabelKey]).To(Equal("fc430"))
				Expect(bareMetalInstance.Spec.HostType).To(Equal("fc430"))
				Expect(bareMetalInstance.OwnerReferences).To(HaveLen(1))
				Expect(bareMetalInstance.OwnerReferences[0].Name).To(Equal(updatedPool.Name))
				Expect(bareMetalInstance.OwnerReferences[0].Kind).To(Equal("BareMetalPool"))
			}
		})

		It("should update status.HostSets", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			Expect(updatedPool.Status.HostSets).To(HaveLen(1))
			Expect(updatedPool.Status.HostSets[0].HostType).To(Equal("fc430"))
			Expect(updatedPool.Status.HostSets[0].Replicas).To(Equal(int32(3)))
		})

		It("should handle error when creating BareMetalInstance CR fails", func() {
			// Update pool to have 5 replicas instead of 3
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())
			updatedPool.Spec.HostSets[0].Replicas = 5
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			// Mock create to succeed for first 2 host leases, then fail
			bareMetalInstancesCreated := 0
			mockK8sClient.createFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*osacv1alpha1.BareMetalInstance); ok {
					if bareMetalInstancesCreated >= 2 {
						return errors.New("create host lease failed")
					}
					bareMetalInstancesCreated++
					return mockK8sClient.Client.Create(ctx, obj, opts...)
				}
				return mockK8sClient.Client.Create(ctx, obj, opts...)
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("create host lease failed"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify only 2 host leases were created
			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(2))

			// Verify status reflects the actual number of host leases created (2, not 5)
			Expect(updatedPool.Status.HostSets).To(HaveLen(1))
			Expect(updatedPool.Status.HostSets[0].HostType).To(Equal("fc430"))
			Expect(updatedPool.Status.HostSets[0].Replicas).To(Equal(int32(2)))

			// Verify error condition is set
			condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
			Expect(condition.Message).To(Equal("Failed to create BareMetalInstance CR"))
		})

		Context("When provisioning provider is nil", func() {
			BeforeEach(func() {
				reconciler = NewBareMetalPoolReconciler(
					mockK8sClient,
					k8sClient.Scheme(),
					nil,
					DefaultHostReadyPollIntervalDuration,
					DefaultHostDeletionPollIntervalDuration,
					DefaultAAPStatusPollIntervalDuration,
					DefaultMaxJobHistory,
				)

				testPoolName = "test-pool-nil-provider"
				testPool = &osacv1alpha1.BareMetalPool{
					ObjectMeta: metav1.ObjectMeta{
						Name:       testPoolName,
						Namespace:  testNamespace,
						Finalizers: []string{BareMetalPoolFinalizer},
					},
					Spec: osacv1alpha1.BareMetalPoolSpec{
						HostSets: []osacv1alpha1.BareMetalHostSet{
							{
								HostType: "fc430",
								Replicas: 2,
							},
						},
						Profile: &osacv1alpha1.ProfileSpec{
							Name: "test",
						},
					},
				}

				Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
			})

			It("should not allocate hosts", func() {
				_, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      testPoolName,
						Namespace: testNamespace,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedPool := &osacv1alpha1.BareMetalPool{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				}, updatedPool)).To(Succeed())

				bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
				err = k8sClient.List(ctx, bareMetalInstanceList,
					client.InNamespace(testNamespace),
					client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(bareMetalInstanceList.Items).To(BeEmpty())

				condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
				Expect(condition).NotTo(BeNil())
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
				Expect(condition.Message).To(Equal("Provisioning provider not configured"))
			})
		})
	})

	Context("When scaling down host leases", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-scale-down"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 3,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())

			// Initial reconcile to create host leases
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should delete BareMetalInstance CRs when replicas are reduced", func() {
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			updatedPool.Spec.HostSets[0].Replicas = 1
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(1))
		})

		It("should handle error when deleting BareMetalInstance CR during scale-down", func() {
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Update to scale down from 3 to 1
			updatedPool.Spec.HostSets[0].Replicas = 1
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			// Mock delete to fail after first successful deletion
			bareMetalInstancesDeleted := 0
			mockK8sClient.deleteFunc = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*osacv1alpha1.BareMetalInstance); ok {
					if bareMetalInstancesDeleted >= 1 {
						return errors.New("delete host lease failed")
					}
					bareMetalInstancesDeleted++
					return mockK8sClient.Client.Delete(ctx, obj, opts...)
				}
				return mockK8sClient.Client.Delete(ctx, obj, opts...)
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("delete host lease failed"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify only 1 host lease was deleted (2 remain)
			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(2))

			// Verify status reflects actual host lease count (2, not 1)
			Expect(updatedPool.Status.HostSets).To(HaveLen(1))
			Expect(updatedPool.Status.HostSets[0].Replicas).To(Equal(int32(2)))

			// Verify error condition is set
			condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
			Expect(condition.Message).To(Equal("Failed to delete BareMetalInstance CR"))
		})
	})

	Context("When scaling up host leases", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-scale-up"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 2,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())

			// Initial reconcile to create host leases
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create additional BareMetalInstance CRs when replicas are increased", func() {
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			updatedPool.Spec.HostSets[0].Replicas = 5
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(5))

			// Verify status is updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())
			Expect(updatedPool.Status.HostSets).To(HaveLen(1))
			Expect(updatedPool.Status.HostSets[0].Replicas).To(Equal(int32(5)))
		})

		It("should handle error when creating BareMetalInstance CR during scale-up", func() {
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Update to scale up from 2 to 5
			updatedPool.Spec.HostSets[0].Replicas = 5
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			// Mock create to succeed for first 2 additional host leases, then fail
			bareMetalInstancesCreated := 0
			mockK8sClient.createFunc = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*osacv1alpha1.BareMetalInstance); ok {
					if bareMetalInstancesCreated >= 2 {
						return errors.New("create host lease failed")
					}
					bareMetalInstancesCreated++
					return mockK8sClient.Client.Create(ctx, obj, opts...)
				}
				return mockK8sClient.Client.Create(ctx, obj, opts...)
			}

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("create host lease failed"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify only 2 additional host leases were created (4 total)
			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(4))

			// Verify status reflects the actual number of host leases (4, not 5)
			Expect(updatedPool.Status.HostSets).To(HaveLen(1))
			Expect(updatedPool.Status.HostSets[0].HostType).To(Equal("fc430"))
			Expect(updatedPool.Status.HostSets[0].Replicas).To(Equal(int32(4)))

			// Verify error condition is set
			condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
			Expect(condition.Message).To(Equal("Failed to create BareMetalInstance CR"))
		})

		It("should scale up a new hostType from zero (adding new hostType)", func() {
			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Add a new hostType h100 with 3 replicas
			updatedPool.Spec.HostSets = append(updatedPool.Spec.HostSets, osacv1alpha1.BareMetalHostSet{
				HostType: "h100",
				Replicas: 3,
			})
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify original fc430 host leases still exist (2)
			fc430BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, fc430BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "fc430",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(fc430BareMetalInstances.Items).To(HaveLen(2))

			// Verify new h100 host leases were created (3)
			h100BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, h100BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "h100",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(h100BareMetalInstances.Items).To(HaveLen(3))

			// Verify status is updated with both hostTypes
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())
			Expect(updatedPool.Status.HostSets).To(HaveLen(2))
		})
	})

	Context("When managing multiple host classes", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-multi-class"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 2,
						},
						{
							HostType: "h100",
							Replicas: 3,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
		})

		It("should create host leases for all host classes", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			fc430BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, fc430BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "fc430",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(fc430BareMetalInstances.Items).To(HaveLen(2))

			h100BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, h100BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "h100",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(h100BareMetalInstances.Items).To(HaveLen(3))
		})

		It("should delete host leases when a host class is removed", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			updatedPool.Spec.HostSets = []osacv1alpha1.BareMetalHostSet{
				{
					HostType: "fc430",
					Replicas: 2,
				},
			}
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			h100BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, h100BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "h100",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(h100BareMetalInstances.Items).To(BeEmpty())
		})

		It("should handle error when deleting host leases for removed host class", func() {
			// First reconcile to create host leases for both classes
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Remove h100 from spec
			updatedPool.Spec.HostSets = []osacv1alpha1.BareMetalHostSet{
				{
					HostType: "fc430",
					Replicas: 2,
				},
			}
			Expect(k8sClient.Update(ctx, updatedPool)).To(Succeed())

			// Mock delete to fail when deleting h100 host leases (after first deletion)
			bareMetalInstancesDeleted := 0
			mockK8sClient.deleteFunc = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				if bareMetalInstance, ok := obj.(*osacv1alpha1.BareMetalInstance); ok {
					if bareMetalInstance.Labels[HostTypeLabelKey] == "h100" {
						if bareMetalInstancesDeleted >= 1 {
							return errors.New("delete host lease failed")
						}
						bareMetalInstancesDeleted++
					}
					return mockK8sClient.Client.Delete(ctx, obj, opts...)
				}
				return mockK8sClient.Client.Delete(ctx, obj, opts...)
			}

			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("delete host lease failed"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify fc430 host leases still exist
			fc430BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, fc430BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "fc430",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(fc430BareMetalInstances.Items).To(HaveLen(2))

			// Verify some h100 host leases were deleted but not all (2 remain)
			h100BareMetalInstances := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, h100BareMetalInstances,
				client.InNamespace(testNamespace),
				client.MatchingLabels{
					BareMetalPoolLabelKey: string(updatedPool.UID),
					HostTypeLabelKey:      "h100",
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(h100BareMetalInstances.Items).To(HaveLen(2))

			// Verify status reflects both host classes still exist
			Expect(updatedPool.Status.HostSets).To(HaveLen(2))

			// Verify error condition is set
			condition := updatedPool.GetStatusCondition(osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
			Expect(condition.Message).To(Equal("Failed to delete BareMetalInstance CR"))
		})
	})

	Context("When BareMetalPool has a profile", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-profile"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 1,
						},
					},
					Profile: &osacv1alpha1.ProfileSpec{
						Name:               "test_profile",
						TemplateParameters: `{"key":"value"}`,
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
		})

		It("should propagate profile template parameters to host leases", func() {
			Expect(profile.LoadProfiles([]*profile.Profile{
				{
					Name:                       "test_profile",
					ExpectedTemplateParameters: []string{"key"},
					BareMetalPoolTemplate:      "test_bmp_template",
					HostTemplate:               "test_hl_template",
				},
			})).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(HaveLen(1))
			Expect(bareMetalInstanceList.Items[0].Spec.TemplateParameters).To(Equal(`{"key":"value"}`))
		})
	})

	Context("When BareMetalPool references a non-existent profile", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-missing-profile"
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 1,
						},
					},
					Profile: &osacv1alpha1.ProfileSpec{
						Name:               "non-existent-profile",
						TemplateParameters: `{"key":"value"}`,
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
		})

		It("should set status condition to Failed with 'Profile does not exist' message", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify the Ready condition is set to False
			readyCondition := getCondition(updatedPool.Status.Conditions, osacv1alpha1.BareMetalPoolConditionTypeReady)
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCondition.Reason).To(Equal(osacv1alpha1.BareMetalPoolReasonFailed))
			Expect(readyCondition.Message).To(Equal("Profile does not exist"))
		})

		It("should not create any BareMetalInstances when profile doesn't exist", func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			updatedPool := &osacv1alpha1.BareMetalPool{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      testPoolName,
				Namespace: testNamespace,
			}, updatedPool)).To(Succeed())

			// Verify no BareMetalInstances were created
			bareMetalInstanceList := &osacv1alpha1.BareMetalInstanceList{}
			err = k8sClient.List(ctx, bareMetalInstanceList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{BareMetalPoolLabelKey: string(updatedPool.UID)},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(bareMetalInstanceList.Items).To(BeEmpty())
		})
	})

	Context("When deleting a BareMetalPool", func() {
		BeforeEach(func() {
			testPoolName = "test-pool-delete"
		})

		It("should unassign host leases and remove finalizer", func() {
			testPool = &osacv1alpha1.BareMetalPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testPoolName,
					Namespace:  testNamespace,
					Finalizers: []string{BareMetalPoolFinalizer},
				},
				Spec: osacv1alpha1.BareMetalPoolSpec{
					HostSets: []osacv1alpha1.BareMetalHostSet{
						{
							HostType: "fc430",
							Replicas: 1,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, testPool)).To(Succeed())
			Expect(k8sClient.Delete(ctx, testPool)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				deletedPool := &osacv1alpha1.BareMetalPool{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testPoolName,
					Namespace: testNamespace,
				}, deletedPool)
				return apierrors.IsNotFound(err)
			}, 5*time.Second).Should(BeTrue())
		})
	})

	Context("When resource does not exist", func() {
		It("should handle not found error gracefully", func() {
			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent-pool",
					Namespace: "test-namespace",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})
})
