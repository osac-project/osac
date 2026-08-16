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

package baremetalhost

import (
	"context"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "osac-baremetal"

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(metal3api.AddToScheme(s)).To(Succeed())
	return s
}

func newTestManager(objects ...client.Object) *Manager {
	k8sClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(objects...).
		WithStatusSubresource(&metal3api.BareMetalHost{}).
		Build()
	return NewManager(k8sClient, testNamespace)
}

var _ = Describe("BareMetalHost Manager", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Namespace", func() {
		It("should return the configured namespace", func() {
			mgr := newTestManager()
			Expect(mgr.Namespace()).To(Equal(testNamespace))
		})
	})

	Describe("CreateBMH", func() {
		It("should create a new BMH", func() {
			mgr := newTestManager()

			err := mgr.CreateBMH(ctx, CreateParams{
				Name:              "node001",
				BMCAddress:        "redfish-virtualmedia+https://10.0.0.1/redfish/v1/Systems/1",
				CredentialsSecret: "bmc-creds",
				BootMACAddress:    "aa:bb:cc:dd:ee:01",
				ConsumerRef: &corev1.ObjectReference{
					APIVersion: "osac.openshift.io/v1alpha1",
					Kind:       "BareMetalInstance",
					Name:       "test-instance-uid",
				},
				Labels: map[string]string{"osac.openshift.io/pool-id": "pool1"},
			})
			Expect(err).NotTo(HaveOccurred())

			bmh := &metal3api.BareMetalHost{}
			Expect(mgr.client.Get(ctx, client.ObjectKey{
				Namespace: testNamespace, Name: "node001",
			}, bmh)).To(Succeed())

			Expect(bmh.Spec.BMC.Address).To(Equal("redfish-virtualmedia+https://10.0.0.1/redfish/v1/Systems/1"))
			Expect(bmh.Spec.BMC.CredentialsName).To(Equal("bmc-creds"))
			Expect(bmh.Spec.BootMACAddress).To(Equal("aa:bb:cc:dd:ee:01"))
			Expect(bmh.Spec.Online).To(BeFalse())
			Expect(bmh.Spec.ConsumerRef).NotTo(BeNil())
			Expect(bmh.Spec.ConsumerRef.Name).To(Equal("test-instance-uid"))
			Expect(bmh.Labels["osac.openshift.io/pool-id"]).To(Equal("pool1"))
		})

		It("should be idempotent with matching consumerRef", func() {
			existing := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Spec: metal3api.BareMetalHostSpec{
					ConsumerRef: &corev1.ObjectReference{
						APIVersion: "osac.openshift.io/v1alpha1",
						Kind:       "BareMetalInstance",
						Name:       "test-instance-uid",
					},
				},
			}
			mgr := newTestManager(existing)

			err := mgr.CreateBMH(ctx, CreateParams{
				Name: "node001",
				ConsumerRef: &corev1.ObjectReference{
					APIVersion: "osac.openshift.io/v1alpha1",
					Kind:       "BareMetalInstance",
					Name:       "test-instance-uid",
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error with different consumerRef", func() {
			existing := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Spec: metal3api.BareMetalHostSpec{
					ConsumerRef: &corev1.ObjectReference{
						Name: "other-instance-uid",
					},
				},
			}
			mgr := newTestManager(existing)

			err := mgr.CreateBMH(ctx, CreateParams{
				Name: "node001",
				ConsumerRef: &corev1.ObjectReference{
					Name: "my-instance-uid",
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("different consumerRef"))
		})

		It("should be idempotent when both consumerRefs are nil", func() {
			existing := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
			}
			mgr := newTestManager(existing)

			err := mgr.CreateBMH(ctx, CreateParams{
				Name: "node001",
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("DeleteBMH", func() {
		It("should delete an existing BMH", func() {
			existing := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
			}
			mgr := newTestManager(existing)

			Expect(mgr.DeleteBMH(ctx, "node001")).To(Succeed())

			bmh := &metal3api.BareMetalHost{}
			err := mgr.client.Get(ctx, client.ObjectKey{
				Namespace: testNamespace, Name: "node001",
			}, bmh)
			Expect(err).To(HaveOccurred())
		})

		It("should be idempotent for not found", func() {
			mgr := newTestManager()
			Expect(mgr.DeleteBMH(ctx, "nonexistent")).To(Succeed())
		})
	})

	Describe("IsBMHReady", func() {
		It("should return true when available and OK", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Status: metal3api.BareMetalHostStatus{
					Provisioning: metal3api.ProvisionStatus{
						State: metal3api.StateAvailable,
					},
					OperationalStatus: metal3api.OperationalStatusOK,
				},
			}
			mgr := newTestManager(bmh)

			ready, err := mgr.IsBMHReady(ctx, "node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeTrue())
		})

		It("should return false when registering", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Status: metal3api.BareMetalHostStatus{
					Provisioning: metal3api.ProvisionStatus{
						State: metal3api.StateRegistering,
					},
					OperationalStatus: metal3api.OperationalStatusOK,
				},
			}
			mgr := newTestManager(bmh)

			ready, err := mgr.IsBMHReady(ctx, "node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeFalse())
		})

		It("should return false when inspecting", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Status: metal3api.BareMetalHostStatus{
					Provisioning: metal3api.ProvisionStatus{
						State: metal3api.StateInspecting,
					},
					OperationalStatus: metal3api.OperationalStatusOK,
				},
			}
			mgr := newTestManager(bmh)

			ready, err := mgr.IsBMHReady(ctx, "node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeFalse())
		})

		It("should return false when preparing", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Status: metal3api.BareMetalHostStatus{
					Provisioning: metal3api.ProvisionStatus{
						State: metal3api.StatePreparing,
					},
					OperationalStatus: metal3api.OperationalStatusOK,
				},
			}
			mgr := newTestManager(bmh)

			ready, err := mgr.IsBMHReady(ctx, "node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(ready).To(BeFalse())
		})

		It("should return error when operational status is error", func() {
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node001",
					Namespace: testNamespace,
				},
				Status: metal3api.BareMetalHostStatus{
					Provisioning: metal3api.ProvisionStatus{
						State: metal3api.StateAvailable,
					},
					OperationalStatus: metal3api.OperationalStatusError,
					ErrorMessage:      "BMC connection failed",
				},
			}
			mgr := newTestManager(bmh)

			_, err := mgr.IsBMHReady(ctx, "node001")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error status"))
		})

		It("should return error when BMH not found", func() {
			mgr := newTestManager()

			_, err := mgr.IsBMHReady(ctx, "nonexistent")
			Expect(err).To(HaveOccurred())
		})
	})
})
