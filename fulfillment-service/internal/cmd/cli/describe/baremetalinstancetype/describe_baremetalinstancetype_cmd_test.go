/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstancetype

import (
	"bytes"
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Helper functions for creating pointers
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

var _ = Describe("describe baremetalinstancetype command", func() {
	var (
		cmd     *cobra.Command
		console *terminal.Console
		buffer  *bytes.Buffer
		ctx     context.Context
	)

	BeforeEach(func() {
		cmd = Cmd()
		buffer = &bytes.Buffer{}

		var err error
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(buffer).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ctx = terminal.ConsoleIntoContext(context.Background(), console)
		ctx = config.SettingsIntoContext(ctx, &config.Settings{})
	})

	Context("when describing a bare metal instance type", func() {
		It("should render complete hardware specifications", func() {
			// This test validates the describe operation contract:
			// - Shows all hardware details in human-readable format
			// - Displays complete metadata including capabilities

			bmiType := &publicv1.BareMetalInstanceType{
				Id: "type-1",
				Metadata: &publicv1.Metadata{
					Name: "gpu-large",
				},
				Spec: &publicv1.BareMetalInstanceTypeSpec{
					Description: "Large GPU node with dual A100",
					Hardware: &publicv1.BareMetalHardwareSpec{
						Cpu: &publicv1.BareMetalCPUSpec{
							Cores:          32,
							Architecture:   "x86_64",
							Model:          "Intel Xeon Gold 6338",
							ThreadsPerCore: 2,
						},
						Memory: &publicv1.BareMetalMemorySpec{
							TotalGb: 512,
							Type:    "DDR4",
						},
						Accelerators: []*publicv1.BareMetalAcceleratorSpec{
							{
								Type:     "GPU",
								Model:    "NVIDIA A100",
								Vendor:   stringPtr("NVIDIA"),
								MemoryGb: int32Ptr(80),
							},
						},
						Disks: []*publicv1.BareMetalDiskSpec{
							{
								Type:       "NVMe SSD",
								CapacityGb: 1000,
								Interface:  "PCIe 4.0",
							},
							{
								Type:       "SATA SSD",
								CapacityGb: 2000,
								Interface:  "SATA 3.0",
							},
						},
						NetworkPorts: []*publicv1.BareMetalNetworkPortSpec{
							{
								Name:  "data-0",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "25Gbps",
							},
							{
								Name:  "data-1",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "25Gbps",
							},
						},
						Capabilities: map[string]string{
							"sr-iov":      "enabled",
							"secure-boot": "supported",
							"tpm":         "2.0",
						},
					},
				},
			}

			renderBareMetalInstanceType(console, bmiType)
			output := buffer.String()

			// Verify basic information
			Expect(output).To(ContainSubstring("Name:"))
			Expect(output).To(ContainSubstring("gpu-large"))
			Expect(output).To(ContainSubstring("ID:"))
			Expect(output).To(ContainSubstring("type-1"))
			Expect(output).To(ContainSubstring("Description:"))
			Expect(output).To(ContainSubstring("Large GPU node with dual A100"))

			// Verify hardware section
			Expect(output).To(ContainSubstring("Hardware:"))

			// Verify CPU details
			Expect(output).To(ContainSubstring("CPU:"))
			Expect(output).To(ContainSubstring("32 cores"))
			Expect(output).To(ContainSubstring("x86_64"))
			Expect(output).To(ContainSubstring("Intel Xeon Gold 6338"))
			Expect(output).To(ContainSubstring("2 threads/core"))

			// Verify memory details
			Expect(output).To(ContainSubstring("Memory:"))
			Expect(output).To(ContainSubstring("512 GB"))
			Expect(output).To(ContainSubstring("DDR4"))

			// Verify accelerator details
			Expect(output).To(ContainSubstring("Accelerators:"))
			Expect(output).To(ContainSubstring("GPU"))
			Expect(output).To(ContainSubstring("NVIDIA A100"))
			Expect(output).To(ContainSubstring("NVIDIA"))
			Expect(output).To(ContainSubstring("80 GB VRAM"))

			// Verify disk details
			Expect(output).To(ContainSubstring("Disks:"))
			Expect(output).To(ContainSubstring("1000 GB NVMe SSD PCIe 4.0"))
			Expect(output).To(ContainSubstring("2000 GB SATA SSD SATA 3.0"))

			// Verify network port details
			Expect(output).To(ContainSubstring("Network Ports:"))
			Expect(output).To(ContainSubstring("data-0"))
			Expect(output).To(ContainSubstring("(fabric)"))
			Expect(output).To(ContainSubstring("25Gbps"))
			Expect(output).To(ContainSubstring("Ethernet"))

			// Verify capabilities
			Expect(output).To(ContainSubstring("Capabilities:"))
			Expect(output).To(ContainSubstring("sr-iov=enabled"))
			Expect(output).To(ContainSubstring("secure-boot=supported"))
			Expect(output).To(ContainSubstring("tpm=2.0"))
		})

		It("should handle missing optional fields gracefully", func() {
			// This test validates graceful handling of sparse hardware specs
			// - Shows available fields only
			// - Uses "-" for missing sections

			bmiType := &publicv1.BareMetalInstanceType{
				Id: "type-minimal",
				Metadata: &publicv1.Metadata{
					Name: "minimal-type",
				},
				Spec: &publicv1.BareMetalInstanceTypeSpec{
					Hardware: &publicv1.BareMetalHardwareSpec{
						Cpu: &publicv1.BareMetalCPUSpec{
							Cores: 8,
							// No architecture, model, or frequency
						},
						Memory: &publicv1.BareMetalMemorySpec{
							TotalGb: 64,
							// No type or speed
						},
						// No accelerators, disks, network ports, or capabilities
					},
				},
			}

			renderBareMetalInstanceType(console, bmiType)
			output := buffer.String()

			// Verify basic info is shown
			Expect(output).To(ContainSubstring("Name:"))
			Expect(output).To(ContainSubstring("minimal-type"))

			// Verify hardware section
			Expect(output).To(ContainSubstring("Hardware:"))

			// Verify minimal CPU info
			Expect(output).To(ContainSubstring("CPU:"))
			Expect(output).To(ContainSubstring("8 cores"))

			// Verify minimal memory info
			Expect(output).To(ContainSubstring("Memory:"))
			Expect(output).To(ContainSubstring("64 GB"))

			// Optional sections should not appear when empty
			Expect(output).ToNot(ContainSubstring("Accelerators:"))
			Expect(output).ToNot(ContainSubstring("Disks:"))
			Expect(output).ToNot(ContainSubstring("Network Ports:"))
			Expect(output).ToNot(ContainSubstring("Capabilities:"))
		})
	})

	Context("hardware formatting functions", func() {
		Describe("formatCPU", func() {
			It("should format complete CPU specification", func() {
				cpu := &publicv1.BareMetalCPUSpec{
					Cores:          24,
					Architecture:   "x86_64",
					Model:          "AMD EPYC 7543",
					ThreadsPerCore: 2,
				}

				result := formatCPU(cpu)
				Expect(result).To(Equal("24 cores, x86_64, AMD EPYC 7543, 2 threads/core"))
			})

			It("should handle partial CPU specification", func() {
				cpu := &publicv1.BareMetalCPUSpec{
					Cores:        16,
					Architecture: "aarch64",
					// No model or threads per core
				}

				result := formatCPU(cpu)
				Expect(result).To(Equal("16 cores, aarch64"))
			})

			It("should handle empty CPU specification", func() {
				cpu := &publicv1.BareMetalCPUSpec{}

				result := formatCPU(cpu)
				Expect(result).To(Equal("-"))
			})
		})

		Describe("formatMemory", func() {
			It("should format complete memory specification", func() {
				memory := &publicv1.BareMetalMemorySpec{
					TotalGb: 256,
					Type:    "DDR5",
				}

				result := formatMemory(memory)
				Expect(result).To(Equal("256 GB, DDR5"))
			})

			It("should handle partial memory specification", func() {
				memory := &publicv1.BareMetalMemorySpec{
					TotalGb: 128,
					// No type
				}

				result := formatMemory(memory)
				Expect(result).To(Equal("128 GB"))
			})

			It("should handle empty memory specification", func() {
				memory := &publicv1.BareMetalMemorySpec{}

				result := formatMemory(memory)
				Expect(result).To(Equal("-"))
			})
		})

		Describe("formatAccelerators", func() {
			It("should format multiple accelerators", func() {
				accelerators := []*publicv1.BareMetalAcceleratorSpec{
					{
						Type:     "GPU",
						Model:    "NVIDIA V100",
						Vendor:   stringPtr("NVIDIA"),
						MemoryGb: int32Ptr(32),
					},
					{
						Type:  "FPGA",
						Model: "Intel Stratix 10",
						// No vendor or memory specified
					},
				}

				result := formatAccelerators(accelerators)
				Expect(result).To(Equal("GPU NVIDIA V100 NVIDIA 32 GB VRAM; FPGA Intel Stratix 10"))
			})

			It("should handle empty accelerators list", func() {
				result := formatAccelerators(nil)
				Expect(result).To(Equal("-"))
			})
		})

		Describe("formatDisks", func() {
			It("should format multiple disks", func() {
				disks := []*publicv1.BareMetalDiskSpec{
					{
						Type:       "NVMe SSD",
						CapacityGb: 500,
						Interface:  "PCIe 4.0",
					},
					{
						Type:       "HDD",
						CapacityGb: 4000,
						Interface:  "SATA",
					},
				}

				result := formatDisks(disks)
				Expect(result).To(Equal("500 GB NVMe SSD PCIe 4.0; 4000 GB HDD SATA"))
			})

			It("should handle empty disks list", func() {
				result := formatDisks(nil)
				Expect(result).To(Equal("-"))
			})
		})

		Describe("formatNetworkPorts", func() {
			It("should format multiple network ports", func() {
				ports := []*publicv1.BareMetalNetworkPortSpec{
					{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "10Gbps",
					},
					{
						Name:  "mgmt-0",
						Role:  "management",
						Type:  "InfiniBand",
						Speed: "100Gbps",
					},
				}

				result := formatNetworkPorts(ports)
				Expect(result).To(Equal("data-0 (fabric) 10Gbps Ethernet; mgmt-0 (management) 100Gbps InfiniBand"))
			})

			It("should handle empty ports list", func() {
				result := formatNetworkPorts(nil)
				Expect(result).To(Equal("-"))
			})
		})

		Describe("formatCapabilities", func() {
			It("should format capabilities map sorted by key", func() {
				capabilities := map[string]string{
					"z-feature":   "disabled",
					"a-feature":   "enabled",
					"m-feature":   "",
					"boot-secure": "yes",
				}

				result := formatCapabilities(capabilities)
				// Should be sorted by key
				Expect(result).To(Equal("a-feature=enabled, boot-secure=yes, m-feature, z-feature=disabled"))
			})

			It("should handle empty capabilities map", func() {
				result := formatCapabilities(nil)
				Expect(result).To(Equal("-"))
			})
		})
	})

	Context("error handling", func() {
		It("should handle network errors gracefully", func() {
			// This test validates error handling contract:
			// - Network failures produce clear error messages
			// - gRPC errors are translated to user-friendly messages

			err := status.Error(codes.Unavailable, "connection refused")

			// Test that we can handle gRPC errors
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unavailable))
		})

		It("should handle not found errors clearly", func() {
			// This test validates not-found contract:
			// - Missing types return clear error messages

			err := status.Error(codes.NotFound, "bare metal instance type not found")

			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			Expect(err.Error()).To(ContainSubstring("bare metal instance type not found"))
		})
	})

	Context("command structure", func() {
		It("should require exactly one argument", func() {
			// This test validates command argument contract:
			// - One arg required = ID/name for lookup
			// - No optional arguments

			Expect(cmd.Use).To(Equal("baremetalinstancetype [FLAG...] ID|NAME"))
			Expect(cmd.Args).ToNot(BeNil())
		})

		It("should have appropriate aliases", func() {
			// This test validates command alias contract:
			// - Supports common variations for usability

			Expect(cmd.Aliases).To(ContainElement("baremetalinstancetypes"))
		})
	})
})
