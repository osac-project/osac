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
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func newTestConsole() *terminal.Console {
	console, err := terminal.NewConsole().
		SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetStdout(io.Discard).
		SetStderr(io.Discard).
		Build()
	if err != nil {
		panic("failed to build test console: " + err.Error())
	}
	return console
}

var _ = Describe("buildSpec", func() {
	It("should populate a registered SSH key reference from the flag", func() {
		c := &runnerContext{}
		c.args.sshKey = "login-key"

		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetSshKey().GetName()).To(Equal("login-key"))
		Expect(spec.GetSshKey().GetId()).To(BeEmpty())
		Expect(spec.GetSshKey().GetProject()).To(BeEmpty())
		Expect(spec.GetSshKey().GetShared()).To(BeFalse())
	})

	It("should parse name-only SSH key syntax for template creation", func() {
		c := &runnerContext{}
		c.args.sshKey = "name=login-key"

		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetSshKey().GetName()).To(Equal("login-key"))
		Expect(spec.GetSshKey().GetProject()).To(BeEmpty())
	})

	It("should preserve the SSH key project from key/value syntax", func() {
		c := &runnerContext{}
		c.args.sshKey = "name=login-key,project=apps"

		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetSshKey().GetName()).To(Equal("login-key"))
		Expect(spec.GetSshKey().GetProject()).To(Equal("apps"))
		Expect(spec.GetSshKey().GetShared()).To(BeFalse())
	})

	It("should send the resolved instance type ID", func() {
		c := &runnerContext{}
		c.args.instanceType = "instance-type-id"
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetInstanceType().GetId()).To(Equal("instance-type-id"))
	})

	It("should populate attachments when network-attachment flags are set", func() {
		c := &runnerContext{
			networkAttachment: publicv1.ComputeNetworkAttachment_builder{
				Subnet:         publicv1.SubnetLocalReference_builder{Id: "n2"}.Build(),
				SecurityGroups: []*publicv1.SecurityGroupLocalReference{publicv1.SecurityGroupLocalReference_builder{Id: "g1"}.Build()},
			}.Build(),
		}
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())

		want := publicv1.ComputeInstanceSpec_builder{
			Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "tmpl"}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
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

	It("should set user_data_source.secret when user-data-secret is provided", func() {
		c := &runnerContext{}
		c.args.userDataSecret = "cloud-init"
		spec, err := c.buildSpec("tmpl", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.GetUserDataSource().GetSecret().GetName()).To(Equal("cloud-init"))
	})
})

var _ = Describe("buildSpecFromCatalogItem", func() {
	DescribeTable("should preserve bare and name-only SSH key values",
		func(value string) {
			c := &runnerContext{}
			c.args.sshKey = value

			spec, err := c.buildSpecFromCatalogItem("cat-001")
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.GetSshKey().GetName()).To(Equal("login-key"))
			Expect(spec.GetSshKey().GetProject()).To(BeEmpty())
		},
		Entry("bare name", "login-key"),
		Entry("name key", "name=login-key"),
	)

	It("should preserve the SSH key project from key/value syntax", func() {
		c := &runnerContext{}
		c.args.sshKey = "name=login-key,project=apps"

		spec, err := c.buildSpecFromCatalogItem("cat-001")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetSshKey().GetName()).To(Equal("login-key"))
		Expect(spec.GetSshKey().GetProject()).To(Equal("apps"))
		Expect(spec.GetSshKey().GetShared()).To(BeFalse())
	})

	It("should send the resolved instance type ID", func() {
		c := &runnerContext{}
		c.args.instanceType = "instance-type-id"
		spec, err := c.buildSpecFromCatalogItem("cat-001")
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.GetInstanceType().GetId()).To(Equal("instance-type-id"))
	})

	It("should populate attachments when network-attachment flags are set", func() {
		c := &runnerContext{
			networkAttachment: publicv1.ComputeNetworkAttachment_builder{
				Subnet:         publicv1.SubnetLocalReference_builder{Id: "n2"}.Build(),
				SecurityGroups: []*publicv1.SecurityGroupLocalReference{publicv1.SecurityGroupLocalReference_builder{Id: "g1"}.Build()},
			}.Build(),
		}
		spec, err := c.buildSpecFromCatalogItem("cat-001")
		Expect(err).NotTo(HaveOccurred())

		want := publicv1.ComputeInstanceSpec_builder{
			CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "cat-001"}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
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

	It("should reject more than one network-attachment flag", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"n1", "n2"}
		err := c.resolveNetworkAttachment(context.Background(), nil)
		Expect(err).To(MatchError(ContainSubstring("at most one --network-attachment")))
	})

	It("should reject an invalid network-attachment value before resolving names", func() {
		c := &runnerContext{}
		c.args.networkAttachments = []string{"subnet=a,foo=bar"}
		err := c.resolveNetworkAttachment(context.Background(), nil)
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

	It("should set user_data_source.secret when user-data-secret is provided", func() {
		c := &runnerContext{}
		c.args.userDataSecret = "cloud-init"
		spec, err := c.buildSpecFromCatalogItem("cat-006")
		Expect(err).NotTo(HaveOccurred())

		Expect(spec.GetUserDataSource().GetSecret().GetName()).To(Equal("cloud-init"))
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

	It("should register --ssh-key and not the removed inline key flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("ssh-key")).NotTo(BeNil())
		Expect(cmd.Flags().Lookup("ssh-public-key")).To(BeNil())
	})
})

var _ = Describe("Create computeinstance flag validation", func() {
	It("should reject user data and a user data secret together", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--template", "tpl-001", "--user-data", "data", "--user-data-secret", "cloud-init"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("user-data"))
		Expect(err.Error()).To(ContainSubstring("user-data-secret"))
	})

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

	It("should return disk with only storage tier when size is not set", func() {
		c := &runnerContext{}
		c.args.bootDiskStorageTier = "premium"
		disk, err := c.buildBootDisk()
		Expect(err).NotTo(HaveOccurred())
		Expect(disk).NotTo(BeNil())
		Expect(disk.GetStorageTier().GetName()).To(Equal("premium"))
		Expect(disk.HasSizeGib()).To(BeFalse())
	})

	It("should return disk with both fields when both are set", func() {
		c := &runnerContext{}
		c.args.bootDiskSizeGiB = 100
		c.args.bootDiskStorageTier = "premium"
		disk, err := c.buildBootDisk()
		Expect(err).NotTo(HaveOccurred())
		Expect(disk).NotTo(BeNil())
		Expect(disk.GetSizeGib()).To(Equal(int32(100)))
		Expect(disk.GetStorageTier().GetName()).To(Equal("premium"))
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

	It("should preserve an explicitly empty value to opt out of catalog defaults", func() {
		raw := rawDisks("--additional-disk", "")
		Expect(raw).To(Equal([]string{""}))

		disks, err := parseAdditionalDisks(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(disks).NotTo(BeNil())
		Expect(disks.GetItems()).To(BeEmpty())
	})

	It("should keep a single comma-joined key=value spec as one element", func() {
		raw := rawDisks("--additional-disk", "size=50,storage-tier=e2e-x")
		Expect(raw).To(Equal([]string{"size=50,storage-tier=e2e-x"}))

		disks, err := parseAdditionalDisks(raw)
		Expect(err).ToNot(HaveOccurred())
		Expect(disks.GetItems()).To(HaveLen(1))
		Expect(disks.GetItems()[0].GetSizeGib()).To(Equal(int32(50)))
		Expect(disks.GetItems()[0].GetStorageTier().GetName()).To(Equal("e2e-x"))
	})

	It("should parse multiple disks with different storage tiers", func() {
		raw := rawDisks(
			"--additional-disk", "size=50,storage-tier=fast",
			"--additional-disk", "size=100,storage-tier=archive",
		)
		Expect(raw).To(Equal([]string{
			"size=50,storage-tier=fast",
			"size=100,storage-tier=archive",
		}))

		disks, err := parseAdditionalDisks(raw)
		Expect(err).ToNot(HaveOccurred())
		Expect(disks.GetItems()).To(HaveLen(2))
		Expect(disks.GetItems()[0].GetSizeGib()).To(Equal(int32(50)))
		Expect(disks.GetItems()[0].GetStorageTier().GetName()).To(Equal("fast"))
		Expect(disks.GetItems()[1].GetSizeGib()).To(Equal(int32(100)))
		Expect(disks.GetItems()[1].GetStorageTier().GetName()).To(Equal("archive"))
	})

	It("should reject a bare integer additional disk", func() {
		raw := rawDisks(
			"--additional-disk", "size=50,storage-tier=e2e-x",
			"--additional-disk", "100",
		)
		Expect(raw).To(Equal([]string{"size=50,storage-tier=e2e-x", "100"}))

		_, err := parseAdditionalDisks(raw)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("storage-tier=<name>"))
	})

	It("should reject an additional disk without a storage tier", func() {
		_, err := parseAdditionalDisks([]string{"size=100"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("storage-tier=<name>"))
	})

	It("should reject an additional disk with an empty storage tier", func() {
		_, err := parseAdditionalDisks([]string{"size=100,storage-tier="})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("value is empty"))
	})
})

var _ = Describe("maybeAttachExistingExternalIP", func() {
	var (
		ctx      context.Context
		ctrl     *gomock.Controller
		runner   *runnerContext
		mockEIPs *MockExternalIPsClient
		mockCIs  *MockComputeInstancesClient
		mockAtts *MockExternalIPAttachmentsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockEIPs = NewMockExternalIPsClient(ctrl)
		mockCIs = NewMockComputeInstancesClient(ctrl)
		mockAtts = NewMockExternalIPAttachmentsClient(ctrl)
		runner = &runnerContext{
			console:                     newTestConsole(),
			externalIPsClient:           mockEIPs,
			computeInstancesClient:      mockCIs,
			externalIPAttachmentsClient: mockAtts,
		}
		runner.args.timeout = 5 * time.Second
	})

	AfterEach(func() { ctrl.Finish() })

	ci := func(id, name string) *publicv1.ComputeInstance {
		return publicv1.ComputeInstance_builder{
			Id:       id,
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		}.Build()
	}

	eip := func(id, name string, state publicv1.ExternalIPState) *publicv1.ExternalIP {
		return publicv1.ExternalIP_builder{
			Id:       id,
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
			Status: publicv1.ExternalIPStatus_builder{
				State:   state,
				Address: "203.0.113.5",
			}.Build(),
		}.Build()
	}

	attachment := func(id string) *publicv1.ExternalIPAttachment {
		return publicv1.ExternalIPAttachment_builder{Id: id}.Build()
	}

	It("should be a no-op when --externalip is not set", func() {
		runner.args.externalIP = ""
		Expect(runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))).To(Succeed())
	})

	It("should resolve EIP, wait for RUNNING, and create attachment on happy path", func() {
		runner.args.externalIP = "my-ip"
		allocatedEIP := eip("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		runningCI := ci("ci-1", "my-vm")
		runningCIStatus := publicv1.ComputeInstance_builder{
			Id:       "ci-1",
			Metadata: publicv1.Metadata_builder{Name: "my-vm"}.Build(),
			Status: publicv1.ComputeInstanceStatus_builder{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			}.Build(),
		}.Build()

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{
				Items: []*publicv1.ExternalIP{allocatedEIP},
			}.Build(), nil)
		mockCIs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: runningCIStatus}.Build(), nil)
		mockAtts.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPAttachmentsCreateResponse_builder{
				Object: attachment("att-1"),
			}.Build(), nil)

		Expect(runner.maybeAttachExistingExternalIP(ctx, runningCI)).To(Succeed())
	})

	It("should fail early when EIP is not ALLOCATED", func() {
		runner.args.externalIP = "my-ip"
		pendingEIP := eip("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{
				Items: []*publicv1.ExternalIP{pendingEIP},
			}.Build(), nil)

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ALLOCATED"))
		Expect(err.Error()).To(ContainSubstring("my-ip"))
	})

	It("should return error when EIP lookup returns empty list", func() {
		runner.args.externalIP = "missing-ip"

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{Items: nil}.Build(), nil)

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
	})

	It("should return error when EIP lookup RPC fails", func() {
		runner.args.externalIP = "my-ip"

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("rpc error"))

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("rpc error"))
	})

	It("should return error when CI poll returns terminal FAILED state", func() {
		runner.args.externalIP = "my-ip"
		allocatedEIP := eip("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		failedCI := publicv1.ComputeInstance_builder{
			Id:       "ci-1",
			Metadata: publicv1.Metadata_builder{Name: "my-vm"}.Build(),
			Status: publicv1.ComputeInstanceStatus_builder{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED,
			}.Build(),
		}.Build()

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{Items: []*publicv1.ExternalIP{allocatedEIP}}.Build(), nil)
		mockCIs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: failedCI}.Build(), nil)

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("RUNNING"))
	})

	It("should return error when attachment creation fails", func() {
		runner.args.externalIP = "my-ip"
		allocatedEIP := eip("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		runningCIStatus := publicv1.ComputeInstance_builder{
			Id:       "ci-1",
			Metadata: publicv1.Metadata_builder{Name: "my-vm"}.Build(),
			Status: publicv1.ComputeInstanceStatus_builder{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			}.Build(),
		}.Build()

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{Items: []*publicv1.ExternalIP{allocatedEIP}}.Build(), nil)
		mockCIs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: runningCIStatus}.Build(), nil)
		mockAtts.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("attach failed"))

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("attach failed"))
	})

	It("should return error when CI poll times out", func() {
		runner.args.externalIP = "my-ip"
		runner.args.timeout = 1 * time.Millisecond
		allocatedEIP := eip("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		pendingCI := publicv1.ComputeInstance_builder{
			Id:       "ci-1",
			Metadata: publicv1.Metadata_builder{Name: "my-vm"}.Build(),
			Status: publicv1.ComputeInstanceStatus_builder{
				State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
			}.Build(),
		}.Build()

		mockEIPs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsListResponse_builder{Items: []*publicv1.ExternalIP{allocatedEIP}}.Build(), nil)
		mockCIs.EXPECT().Get(gomock.Any(), gomock.Any()).AnyTimes().Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: pendingCI}.Build(), nil)

		err := runner.maybeAttachExistingExternalIP(ctx, ci("ci-1", "my-vm"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("RUNNING"))
	})
})
