/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("parseNetworkAttachmentFlag", func() {
	DescribeTable("when input is valid it should parse",
		func(input, wantSubnet string, wantSGs []string) {
			got, err := parseNetworkAttachmentFlag(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetSubnet().GetId()).To(Equal(wantSubnet))
			var gotSGs []string
			for _, sg := range got.GetSecurityGroups() {
				gotSGs = append(gotSGs, sg.GetId())
			}
			if wantSGs == nil {
				Expect(gotSGs).To(BeNil())
			} else {
				Expect(gotSGs).To(Equal(wantSGs))
			}
		},
		Entry("when value is bare subnet id it should use it as subnet",
			"  sub-1  ", "sub-1", nil),
		Entry("when value is subnet=key form it should parse subnet",
			"subnet=sub-2", "sub-2", nil),
		Entry("when value includes security_groups alias it should parse groups",
			"subnet=a,security_groups=g1,g2", "a", []string{"g1", "g2"}),
		Entry("when value lists security-groups after subnet it should parse groups",
			"subnet=b,security-groups=x", "b", []string{"x"}),
	)

	DescribeTable("when input is invalid it should error",
		func(input string) {
			_, err := parseNetworkAttachmentFlag(input)
			Expect(err).To(HaveOccurred())
		},
		Entry("when value is empty it should error", "   "),
		Entry("when key is unknown it should error", "subnet=a,foo=bar"),
		Entry("when subnet= form omits subnet it should error", "security-groups=g1"),
		Entry("when fragment has no equals it should error", "subnet=sub,noglue"),
		Entry("when value after equals is empty it should error", "subnet="),
		Entry("when subnet is duplicated it should error", "subnet=a,subnet=b"),
	)
})

// Legacy subnet and security-groups tests removed - these fields are no longer supported

var _ = Describe("buildSpec", func() {
	It("should populate attachments when network-attachment flags are set", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"n1", "subnet=n2,security-groups=g1"}
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())

		want := publicv1.ComputeInstanceSpec_builder{
			Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tmpl"}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
				publicv1.ComputeNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Id: "n1"}.Build()}.Build(),
				publicv1.ComputeNetworkAttachment_builder{
					Subnet:         publicv1.SubnetLocalReference_builder{Id: "n2"}.Build(),
					SecurityGroups: []*publicv1.SecurityGroupLocalReference{publicv1.SecurityGroupLocalReference_builder{Id: "g1"}.Build()},
				}.Build(),
			},
		}.Build()
		Expect(proto.Equal(spec, want)).To(BeTrue(), "spec should equal expected spec")
	})

	It("should set disk_image when disk-image flag is provided", func() {
		c := &runnerContext{}
		c.args.diskImage = "my-disk-image"
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.HasDiskImage()).To(BeTrue())
		Expect(spec.GetDiskImage().GetName()).To(Equal("my-disk-image"))
	})

	It("should leave disk_image unset when disk-image flag is empty", func() {
		c := &runnerContext{}
		c.args.diskImage = ""
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.HasDiskImage()).To(BeFalse())
	})
})

var _ = Describe("buildSpecFromCatalogItem", func() {
	It("should populate attachments when network-attachment flags are set", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"n1", "subnet=n2,security-groups=g1"}
		spec, err := c.buildSpecFromCatalogItem("cat-001")
		Expect(err).NotTo(HaveOccurred())

		want := publicv1.ComputeInstanceSpec_builder{
			CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "cat-001"}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
				publicv1.ComputeNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Id: "n1"}.Build()}.Build(),
				publicv1.ComputeNetworkAttachment_builder{
					Subnet:         publicv1.SubnetLocalReference_builder{Id: "n2"}.Build(),
					SecurityGroups: []*publicv1.SecurityGroupLocalReference{publicv1.SecurityGroupLocalReference_builder{Id: "g1"}.Build()},
				}.Build(),
			},
		}.Build()
		Expect(proto.Equal(spec, want)).To(BeTrue(), "spec should equal expected spec")
	})

	It("should return spec without attachments when no network flags are set", func() {
		c := &runnerContext{}
		spec, err := c.buildSpecFromCatalogItem("cat-002")
		Expect(err).NotTo(HaveOccurred())

		want := publicv1.ComputeInstanceSpec_builder{
			CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "cat-002"}.Build(),
		}.Build()
		Expect(proto.Equal(spec, want)).To(BeTrue(), "spec should equal expected spec")
	})

	It("should return error when network-attachment value is invalid", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"subnet=a,foo=bar"}
		_, err := c.buildSpecFromCatalogItem("cat-003")
		Expect(err).To(HaveOccurred())
	})

	It("should set disk_image when disk-image flag is provided", func() {
		c := &runnerContext{}
		c.args.diskImage = "my-disk-image"
		spec, err := c.buildSpecFromCatalogItem("cat-004")
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.HasDiskImage()).To(BeTrue())
		Expect(spec.GetDiskImage().GetName()).To(Equal("my-disk-image"))
	})

	It("should leave disk_image unset when disk-image flag is empty", func() {
		c := &runnerContext{}
		c.args.diskImage = ""
		spec, err := c.buildSpecFromCatalogItem("cat-005")
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.HasDiskImage()).To(BeFalse())
	})
})

var _ = Describe("Create computeinstance flag registration", func() {
	It("should register --catalog-item flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Catalog item"))
	})

	It("should still register --template flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --catalog-item without a short flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(BeEmpty())
	})

	It("should keep -t as shorthand for --template", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(Equal("t"))
	})

	It("should register --disk-image flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("disk-image")
		Expect(flag).NotTo(BeNil())
	})
})

var _ = Describe("Create computeinstance flag validation", func() {
	It("should return error when both --catalog-item and --template are set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--catalog-item", "cat-001", "--template", "tpl-001", "--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("if any flags in the group"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})

	It("should return error when neither --catalog-item nor --template is set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one of the flags"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})
})

var _ = Describe("buildBootDisk", func() {
	It("should return nil when neither size nor storage tier is set", func() {
		c := &runnerContext{}
		disk, err := c.buildBootDisk()
		Expect(err).NotTo(HaveOccurred())
		Expect(disk).To(BeNil())
	})

	It("should return error when storage tier is set without size", func() {
		c := &runnerContext{}
		c.args.bootDiskStorageTier = "premium"
		_, err := c.buildBootDisk()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("--boot-disk-size is required"))
	})

	It("should return disk with both fields when both are set", func() {
		c := &runnerContext{}
		c.args.bootDiskSizeGiB = 100
		c.args.bootDiskStorageTier = "premium"
		disk, err := c.buildBootDisk()
		Expect(err).NotTo(HaveOccurred())
		Expect(disk).NotTo(BeNil())
		Expect(disk.GetSizeGib()).To(Equal(int32(100)))
		Expect(disk.GetStorageTier()).To(Equal("premium"))
	})

	It("should return disk with only size when storage tier is not set", func() {
		c := &runnerContext{}
		c.args.bootDiskSizeGiB = 50
		disk, err := c.buildBootDisk()
		Expect(err).NotTo(HaveOccurred())
		Expect(disk).NotTo(BeNil())
		Expect(disk.GetSizeGib()).To(Equal(int32(50)))
		Expect(disk.HasStorageTier()).To(BeFalse())
	})
})

var _ = Describe("--additional-disk flag parsing", func() {
	// rawDisks parses the given args on a fresh command and returns the
	// --additional-disk values exactly as Cobra stored them. It reads through
	// pflag's SliceValue interface, which both StringSlice and StringArray
	// implement, so the assertions below can catch the difference in how those
	// two types split (or preserve) the value during Parse().
	rawDisks := func(args ...string) []string {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		Expect(cmd.Flags().Parse(args)).To(Succeed())
		val, ok := cmd.Flags().Lookup("additional-disk").Value.(pflag.SliceValue)
		Expect(ok).To(BeTrue(), "additional-disk flag must expose a slice value")
		return val.GetSlice()
	}

	It("should keep a single comma-joined key=value spec as one element", func() {
		raw := rawDisks("--additional-disk", "size=50,storage-tier=e2e-x")
		Expect(raw).To(Equal([]string{"size=50,storage-tier=e2e-x"}))

		disks, err := parseAdditionalDisks(raw)
		Expect(err).ToNot(HaveOccurred())
		Expect(disks).To(HaveLen(1))
		Expect(disks[0].GetSizeGib()).To(Equal(int32(50)))
		Expect(disks[0].GetStorageTier()).To(Equal("e2e-x"))
	})

	It("should treat each flag occurrence as a distinct disk", func() {
		raw := rawDisks(
			"--additional-disk", "size=50,storage-tier=e2e-x",
			"--additional-disk", "100",
		)
		Expect(raw).To(Equal([]string{"size=50,storage-tier=e2e-x", "100"}))

		disks, err := parseAdditionalDisks(raw)
		Expect(err).ToNot(HaveOccurred())
		Expect(disks).To(HaveLen(2))
		Expect(disks[0].GetSizeGib()).To(Equal(int32(50)))
		Expect(disks[0].GetStorageTier()).To(Equal("e2e-x"))
		Expect(disks[1].GetSizeGib()).To(Equal(int32(100)))
	})
})
