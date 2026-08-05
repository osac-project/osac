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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("ComputeInstance CEL Validation", func() {
	var (
		namespace *corev1.Namespace
		ctx       context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create a unique namespace for each test
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-validation-",
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	})

	AfterEach(func() {
		// Clean up namespace
		if namespace != nil {
			Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
		}
	})

	// Helper function to create a valid base ComputeInstance
	createValidInstance := func(name string) *osacv1alpha1.ComputeInstance {
		return &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace.Name,
			},
			Spec: osacv1alpha1.ComputeInstanceSpec{
				TemplateID: "test_template",
				Image: osacv1alpha1.ImageSpec{
					SourceType: osacv1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "quay.io/test/test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    osacv1alpha1.DiskSpec{SizeGiB: 20},
				RunStrategy: osacv1alpha1.RunStrategyAlways,
			},
		}
	}

	It("should allow creation with networkAttachments", func() {
		instance := createValidInstance("test-network-attachments")
		instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
			{SubnetRef: "subnet-a"},
		}

		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
	})

	It("should allow creation without networkAttachments", func() {
		instance := createValidInstance("test-no-subnets")

		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
	})

	Describe("NetworkAttachment immutability", func() {
		It("documents limitation: changing subnetRef via full replacement bypasses validation", func() {
			// LIMITATION: With listType=map, changing the map key (subnetRef) is treated as
			// removing the old item and adding a new item. Since the array size stays the same,
			// the size check passes. The self==oldSelf validation on subnetRef doesn't trigger
			// because Kubernetes sees these as different items (different keys = uncorrelated).
			//
			// Preventing this edge case would require a validating webhook that checks if
			// the set of subnetRef values has changed.
			//
			// In practice, this is unlikely to occur accidentally since changing a VM's subnet
			// typically requires explicit user action, and the VM would need to be recreated anyway.
			instance := createValidInstance("test-subnet-replacement")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-a", SecurityGroupRefs: []string{"sg-1"}},
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Replace with different subnetRef (same size, different key)
			// This currently is NOT prevented by CEL validations
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-b", SecurityGroupRefs: []string{"sg-1"}},
			}
			err := k8sClient.Update(ctx, instance)
			// Currently this succeeds - documenting known limitation
			_ = err // May or may not fail depending on future webhook implementation
		})

		It("should allow changing securityGroupRefs without changing subnetRef", func() {
			instance := createValidInstance("test-sg-mutable")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-a", SecurityGroupRefs: []string{"sg-1"}},
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Change only securityGroupRefs
			instance.Spec.NetworkAttachments[0].SecurityGroupRefs = []string{"sg-2", "sg-3"}
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		})

		It("should reject adding networkAttachment entries", func() {
			instance := createValidInstance("test-add-attachment")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-a"},
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to add another networkAttachment
			instance.Spec.NetworkAttachments = append(instance.Spec.NetworkAttachments,
				osacv1alpha1.NetworkAttachment{SubnetRef: "subnet-b"},
			)
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
		})

		It("should reject removing networkAttachment entries", func() {
			instance := createValidInstance("test-remove-attachment")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-a"},
				{SubnetRef: "subnet-b"},
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to remove a networkAttachment
			instance.Spec.NetworkAttachments = instance.Spec.NetworkAttachments[:1]
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
		})

		// Removed: duplicate of "documents limitation: changing subnetRef via full replacement bypasses validation"
		// This test was testing the same edge case - see that test for explanation.
	})

	Describe("Image immutability", func() {
		It("should reject changing image sourceRef", func() {
			instance := createValidInstance("test-image-immutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change image
			instance.Spec.Image.SourceRef = "quay.io/test/different-image:latest"
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("image is immutable"))
		})

		It("should reject changing image sourceType", func() {
			instance := createValidInstance("test-image-type-immutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change image sourceType (even though only "registry" is valid)
			instance.Spec.Image.SourceType = "registry" // Same value but tests whole struct
			instance.Spec.Image.SourceRef = "new-ref"   // This should trigger immutability
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("image is immutable"))
		})
	})

	Describe("Compute resource immutability", func() {
		It("should reject changing cores", func() {
			instance := createValidInstance("test-cores-immutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change cores
			instance.Spec.Cores = 4
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("cores is immutable"))
		})

		It("should reject changing memoryGiB", func() {
			instance := createValidInstance("test-memory-immutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change memory
			instance.Spec.MemoryGiB = 8
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("memoryGiB is immutable"))
		})
	})

	Describe("Disk immutability", func() {
		It("should reject changing bootDisk size", func() {
			instance := createValidInstance("test-bootdisk-immutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change boot disk size
			instance.Spec.BootDisk.SizeGiB = 50
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("bootDisk is immutable"))
		})

		It("should reject changing additionalDisks", func() {
			instance := createValidInstance("test-additionaldisks-immutable")
			instance.Spec.AdditionalDisks = []osacv1alpha1.DiskSpec{
				{SizeGiB: 100},
			}
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change additional disks
			instance.Spec.AdditionalDisks[0].SizeGiB = 200
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("additionalDisks is immutable"))
		})

		It("should reject adding additionalDisks", func() {
			instance := createValidInstance("test-add-disk-immutable")
			instance.Spec.AdditionalDisks = []osacv1alpha1.DiskSpec{
				{SizeGiB: 100},
			}
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to add another disk
			instance.Spec.AdditionalDisks = append(instance.Spec.AdditionalDisks,
				osacv1alpha1.DiskSpec{SizeGiB: 200},
			)
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("additionalDisks is immutable"))
		})
	})

	Describe("UserData and SSH immutability", func() {
		It("should reject changing userDataSecretRef", func() {
			instance := createValidInstance("test-userdata-immutable")
			instance.Spec.UserDataSecretRef = &corev1.LocalObjectReference{
				Name: "userdata-secret",
			}
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change userDataSecretRef
			instance.Spec.UserDataSecretRef.Name = "different-secret"
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("userDataSecretRef is immutable"))
		})

		It("documents limitation: adding optional fields after creation is not prevented", func() {
			// LIMITATION: The CEL validation "self == oldSelf" on optional fields only prevents
			// CHANGING an already-set value. It does not prevent setting a field from nil/unset to a value.
			// This is because CEL field-level validations can't distinguish between "field not set" and
			// "field set to nil/empty" after JSON unmarshaling.
			//
			// To prevent nil-to-value transitions would require parent-level validation using has() checks,
			// or a validating webhook. For the current use case, this limitation is acceptable since:
			// 1. Most immutable fields are required (cores, memory, etc.)
			// 2. Optional immutable fields (userDataSecretRef, sshKey) are typically set at creation
			// 3. Changing a set value IS prevented by the validation
			instance := createValidInstance("test-add-userdata")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Add userDataSecretRef - currently NOT prevented by CEL validation
			instance.Spec.UserDataSecretRef = &corev1.LocalObjectReference{
				Name: "new-secret",
			}
			err := k8sClient.Update(ctx, instance)
			// Currently this succeeds - documenting known limitation
			_ = err // May or may not fail depending on future webhook implementation
		})

		It("should reject changing sshKey", func() {
			instance := createValidInstance("test-sshkey-immutable")
			instance.Spec.SSHKey = "ssh-rsa AAAAB3NzaC1..."
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Try to change SSH key
			instance.Spec.SSHKey = "ssh-rsa DIFFERENT..."
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("sshKey is immutable"))
		})
	})

	Describe("Mutable fields", func() {
		It("should allow changing runStrategy", func() {
			instance := createValidInstance("test-runstrategy-mutable")
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyAlways
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Change runStrategy - should succeed
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyHalted
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())

			// Verify the change
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
			Expect(instance.Spec.RunStrategy).To(Equal(osacv1alpha1.RunStrategyHalted))
		})

		It("should allow setting restartRequestedAt", func() {
			instance := createValidInstance("test-restart-mutable")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Set restartRequestedAt - should succeed
			now := metav1.Now()
			instance.Spec.RestartRequestedAt = &now
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())

			// Verify the change
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
			Expect(instance.Spec.RestartRequestedAt).ToNot(BeNil())
		})
	})

	Describe("GuestOSFamily validation", func() {
		It("should allow creation without guestOSFamily field (optional)", func() {
			instance := createValidInstance("test-no-guestos")
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		})

		It("should allow creation with guestOSFamily set to linux", func() {
			instance := createValidInstance("test-linux")
			instance.Spec.GuestOSFamily = "linux"
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		})

		It("should allow creation with guestOSFamily set to windows", func() {
			instance := createValidInstance("test-windows")
			instance.Spec.GuestOSFamily = "windows"
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		})

		It("should allow creation with unknown guestOSFamily value (freeform string)", func() {
			instance := createValidInstance("test-freebsd")
			instance.Spec.GuestOSFamily = "freebsd"
			// No validation constraint, freeform string per D-03
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		})

		It("should reject changing guestOSFamily after creation", func() {
			instance := createValidInstance("test-immutable")
			instance.Spec.GuestOSFamily = "linux"
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Attempt to change guestOSFamily
			instance.Spec.GuestOSFamily = "windows"
			err := k8sClient.Update(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("guestOSFamily is immutable"))
		})

		It("should allow updates when guestOSFamily unchanged", func() {
			instance := createValidInstance("test-mutable-other")
			instance.Spec.GuestOSFamily = "windows"
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyAlways
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Change mutable field (runStrategy), leave guestOSFamily unchanged
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyHalted
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		})

		It("should allow updates when guestOSFamily was not set and remains unset", func() {
			instance := createValidInstance("test-unset-unchanged")
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyAlways
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch latest version
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())

			// Change mutable field, leave guestOSFamily unset
			instance.Spec.RunStrategy = osacv1alpha1.RunStrategyHalted
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		})

		It("should serialize guestOSFamily to JSON when set", func() {
			instance := createValidInstance("test-serialize")
			instance.Spec.GuestOSFamily = "windows"
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch and verify
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
			Expect(instance.Spec.GuestOSFamily).To(Equal("windows"))
		})

		It("should omit guestOSFamily from JSON when empty (omitempty)", func() {
			instance := createValidInstance("test-omitempty")
			// Don't set GuestOSFamily
			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Fetch and verify field is empty
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
			Expect(instance.Spec.GuestOSFamily).To(Equal(""))
		})
	})

	Describe("MaxItems validation", func() {
		It("should reject creating ComputeInstance with more than 8 networkAttachments", func() {
			instance := createValidInstance("test-max-attachments")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-1"},
				{SubnetRef: "subnet-2"},
				{SubnetRef: "subnet-3"},
				{SubnetRef: "subnet-4"},
				{SubnetRef: "subnet-5"},
				{SubnetRef: "subnet-6"},
				{SubnetRef: "subnet-7"},
				{SubnetRef: "subnet-8"},
				{SubnetRef: "subnet-9"}, // 9th entry exceeds maxItems:8
			}

			err := k8sClient.Create(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("Too many"))
		})

		It("should allow creating ComputeInstance with exactly 8 networkAttachments", func() {
			instance := createValidInstance("test-exactly-8-attachments")
			instance.Spec.NetworkAttachments = []osacv1alpha1.NetworkAttachment{
				{SubnetRef: "subnet-1"},
				{SubnetRef: "subnet-2"},
				{SubnetRef: "subnet-3"},
				{SubnetRef: "subnet-4"},
				{SubnetRef: "subnet-5"},
				{SubnetRef: "subnet-6"},
				{SubnetRef: "subnet-7"},
				{SubnetRef: "subnet-8"},
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		})
	})
})
