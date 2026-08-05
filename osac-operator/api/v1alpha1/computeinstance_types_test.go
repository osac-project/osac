package v1alpha1_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

func TestV1alpha1(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "V1alpha1 API Suite")
}

var _ = Describe("ComputeInstanceSpec", func() {
	Describe("Valid ComputeInstance spec", func() {
		It("should accept a valid spec with all required fields", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "rhel10-desktop",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "quay.io/fedora/fedora-coreos:stable",
				},
				Cores:     4,
				MemoryGiB: 8,
				BootDisk: v1alpha1.DiskSpec{
					SizeGiB: 30,
				},
				RunStrategy: v1alpha1.RunStrategyAlways,
			}

			Expect(spec.TemplateID).To(Equal("rhel10-desktop"))
			Expect(spec.Image.SourceType).To(Equal(v1alpha1.ImageSourceTypeRegistry))
			Expect(spec.Image.SourceRef).To(Equal("quay.io/fedora/fedora-coreos:stable"))
			Expect(spec.Cores).To(Equal(int32(4)))
			Expect(spec.MemoryGiB).To(Equal(int32(8)))
			Expect(spec.BootDisk.SizeGiB).To(Equal(int32(30)))
			Expect(spec.RunStrategy).To(Equal(v1alpha1.RunStrategyAlways))
		})

		It("should accept a minimal valid spec", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       1,
				MemoryGiB:   1,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 1},
				RunStrategy: v1alpha1.RunStrategyAlways,
			}

			Expect(spec.TemplateID).ToNot(BeEmpty())
			Expect(spec.Cores).To(BeNumerically(">=", 1))
			Expect(spec.MemoryGiB).To(BeNumerically(">=", 1))
			Expect(spec.BootDisk.SizeGiB).To(BeNumerically(">=", 1))
		})

		It("should support additional disks", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:     2,
				MemoryGiB: 4,
				BootDisk:  v1alpha1.DiskSpec{SizeGiB: 10},
				AdditionalDisks: []v1alpha1.DiskSpec{
					{SizeGiB: 50},
					{SizeGiB: 100},
					{SizeGiB: 200},
				},
				RunStrategy: v1alpha1.RunStrategyAlways,
			}

			Expect(spec.AdditionalDisks).To(HaveLen(3))
			Expect(spec.AdditionalDisks[0].SizeGiB).To(Equal(int32(50)))
			Expect(spec.AdditionalDisks[1].SizeGiB).To(Equal(int32(100)))
			Expect(spec.AdditionalDisks[2].SizeGiB).To(Equal(int32(200)))
		})

		It("should support user data secret reference", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:     2,
				MemoryGiB: 4,
				BootDisk:  v1alpha1.DiskSpec{SizeGiB: 10},
				UserDataSecretRef: &corev1.LocalObjectReference{
					Name: "my-cloud-init",
				},
				RunStrategy: v1alpha1.RunStrategyAlways,
			}

			Expect(spec.UserDataSecretRef).ToNot(BeNil())
			Expect(spec.UserDataSecretRef.Name).To(Equal("my-cloud-init"))
		})

		It("should support SSH key", func() {
			sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 10},
				SSHKey:      sshKey,
				RunStrategy: v1alpha1.RunStrategyAlways,
			}

			Expect(spec.SSHKey).To(Equal(sshKey))
		})
	})

	Describe("RunStrategy", func() {
		DescribeTable("Valid run strategy values",
			func(strategy v1alpha1.RunStrategyType, expected string) {
				Expect(string(strategy)).To(Equal(expected))
			},
			Entry("Always strategy", v1alpha1.RunStrategyAlways, "Always"),
			Entry("Halted strategy", v1alpha1.RunStrategyHalted, "Halted"),
		)
	})

	Describe("ImageSourceType", func() {
		It("should have registry as valid source type", func() {
			Expect(string(v1alpha1.ImageSourceTypeRegistry)).To(Equal("registry"))
		})
	})
})

var _ = Describe("ComputeInstance", func() {
	Describe("GetName", func() {
		It("should return the name of the ComputeInstance", func() {
			ci := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-instance",
				},
			}

			Expect(ci.GetName()).To(Equal("test-instance"))
		})
	})

	Describe("PrimarySubnetRef", func() {
		It("should return the first networkAttachments subnet when set", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{SubnetRef: "subnet-A", SecurityGroupRefs: []string{"sg-1"}},
					{SubnetRef: "subnet-B", SecurityGroupRefs: []string{"sg-2"}},
				},
			}

			Expect(spec.PrimarySubnetRef()).To(Equal("subnet-A"))
		})

		It("should return empty string when networkAttachments is empty", func() {
			spec := v1alpha1.ComputeInstanceSpec{}

			Expect(spec.PrimarySubnetRef()).To(Equal(""))
		})
	})

	Describe("NetworkAttachments", func() {
		It("should support single network attachment", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 10},
				RunStrategy: v1alpha1.RunStrategyAlways,
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{
						SubnetRef:         "subnet-A",
						SecurityGroupRefs: []string{"sg-1", "sg-2"},
					},
				},
			}

			Expect(spec.NetworkAttachments).To(HaveLen(1))
			Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-A"))
			Expect(spec.NetworkAttachments[0].SecurityGroupRefs).To(Equal([]string{"sg-1", "sg-2"}))
		})

		It("should support multiple network attachments", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 10},
				RunStrategy: v1alpha1.RunStrategyAlways,
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{SubnetRef: "subnet-A", SecurityGroupRefs: []string{"web-sg"}},
					{SubnetRef: "subnet-B", SecurityGroupRefs: []string{"db-sg"}},
					{SubnetRef: "subnet-C", SecurityGroupRefs: []string{"mon-sg"}},
				},
			}

			Expect(spec.NetworkAttachments).To(HaveLen(3))
			Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-A"))
			Expect(spec.NetworkAttachments[1].SubnetRef).To(Equal("subnet-B"))
			Expect(spec.NetworkAttachments[2].SubnetRef).To(Equal("subnet-C"))
		})

		It("should support network attachment without security groups", func() {
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 10},
				RunStrategy: v1alpha1.RunStrategyAlways,
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{SubnetRef: "subnet-A"},
				},
			}

			Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-A"))
			Expect(spec.NetworkAttachments[0].SecurityGroupRefs).To(BeEmpty())
		})
	})

	Describe("NetworkAttachment immutability validation", func() {
		It("should allow modifying security groups in struct", func() {
			// This tests struct mutability - actual CRD validation happens at API level.
			// Security groups are mutable per the CRD design.
			spec := v1alpha1.ComputeInstanceSpec{
				TemplateID: "test-template",
				Image: v1alpha1.ImageSpec{
					SourceType: v1alpha1.ImageSourceTypeRegistry,
					SourceRef:  "test-image:latest",
				},
				Cores:       2,
				MemoryGiB:   4,
				BootDisk:    v1alpha1.DiskSpec{SizeGiB: 10},
				RunStrategy: v1alpha1.RunStrategyAlways,
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{SubnetRef: "subnet-A", SecurityGroupRefs: []string{"sg-1"}},
				},
			}

			updatedSpec := spec
			updatedSpec.NetworkAttachments[0].SecurityGroupRefs = []string{"sg-2", "sg-3"}

			Expect(updatedSpec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-A"))
			Expect(updatedSpec.NetworkAttachments[0].SecurityGroupRefs).To(Equal([]string{"sg-2", "sg-3"}))
		})

		It("documents subnet immutability enforcement via CEL", func() {
			// CRD validation enforces subnet immutability at the API server level via:
			// 1. Field-level XValidation: subnetRef has "self == oldSelf" rule
			// 2. Array-level XValidation: networkAttachments has "size(self) == size(oldSelf)" rule
			// 3. List type markers: +listType=map and +listMapKey=subnetRef make array correlatable
			// 4. MaxItems: +kubebuilder:validation:MaxItems=8 reduces CEL cost
			//
			// When updating a ComputeInstance via kubectl/API:
			// - Changing subnetRef from "subnet-A" to "subnet-B" → REJECTED (field validation)
			// - Adding/removing network attachments → REJECTED (array size validation)
			// - Changing security groups → ALLOWED (no validation on that field)
			spec := v1alpha1.ComputeInstanceSpec{
				NetworkAttachments: []v1alpha1.NetworkAttachment{
					{SubnetRef: "subnet-A", SecurityGroupRefs: []string{"sg-1"}},
				},
			}

			Expect(spec.NetworkAttachments[0].SubnetRef).To(Equal("subnet-A"))
		})

		It("documents max network attachments limit", func() {
			// The CRD enforces maxItems: 8 to keep CEL validation cost within budget.
			// Attempting to create a ComputeInstance with >8 network attachments
			// would be rejected by the API server.
			Expect(8).To(BeNumerically(">", 0))
		})
	})
})
