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
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("create baremetalinstancetype command", func() {

	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := Cmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("baremetalinstancetype"))
		})

		It("should have the correct aliases", func() {
			cmd := Cmd()
			Expect(cmd.Aliases).To(ContainElement("osac.private.v1.BareMetalInstanceType"))
		})

		It("should accept no arguments", func() {
			cmd := Cmd()
			Expect(cmd.Args).NotTo(BeNil())
		})
	})

	Context("flag registration", func() {
		It("should register --name flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("name")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("NAME"))
		})

		It("should register --cpu-cores flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("cpu-cores")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("CORES"))
		})

		It("should register --cpu-architecture flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("cpu-architecture")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("ARCHITECTURE"))
		})

		It("should register --memory-total-gb flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("memory-total-gb")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("MEMORY"))
		})

		It("should register optional --description flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("description")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("DESCRIPTION"))
		})

		It("should register optional --cpu-model flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("cpu-model")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("MODEL"))
		})

		It("should register optional --cpu-threads-per-core flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("cpu-threads-per-core")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("THREADS"))
		})

		It("should register optional --memory-type flag", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("memory-type")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("TYPE"))
		})

		It("should register --disk flag for repeatable disk specs", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("disk")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("DISK"))
		})

		It("should register --accelerator flag for repeatable accelerator specs", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("accelerator")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("ACCELERATOR"))
		})

		It("should register --network-port flag for repeatable network port specs", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("network-port")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("NETWORK"))
		})

		It("should register --capability flag for repeatable capability key-value pairs", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("capability")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("CAPABILITY"))
		})

		It("should register required --host-label flag for host label selector", func() {
			cmd := Cmd()
			flag := cmd.Flags().Lookup("host-label")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("LABEL"))
		})
	})

	Context("help text", func() {
		It("should have proper short help", func() {
			cmd := Cmd()
			Expect(cmd.Short).To(Equal("Create a bare metal instance type"))
		})

		It("should have proper long help with examples", func() {
			cmd := Cmd()
			Expect(cmd.Long).To(ContainSubstring("Create a bare metal instance type."))
			Expect(cmd.Long).To(ContainSubstring("create baremetalinstancetype"))
			Expect(cmd.Long).To(ContainSubstring("--name gpu-large"))
			Expect(cmd.Long).To(ContainSubstring("--cpu-cores"))
			Expect(cmd.Long).To(ContainSubstring("--cpu-architecture"))
			Expect(cmd.Long).To(ContainSubstring("--memory-total-gb"))
		})
	})

	Context("flag parsing functions", func() {
		Describe("parseDiskFlag", func() {
			It("should parse valid disk specification", func() {
				disk, err := parseDiskFlag("SSD:1000:NVMe")
				Expect(err).NotTo(HaveOccurred())
				Expect(disk.Type).To(Equal("SSD"))
				Expect(disk.CapacityGb).To(Equal(int64(1000)))
				Expect(disk.Interface).To(Equal("NVMe"))
			})

			It("should reject disk specification with missing fields", func() {
				_, err := parseDiskFlag("SSD:1000")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("format"))
			})

			It("should reject disk specification with invalid capacity", func() {
				_, err := parseDiskFlag("SSD:invalid:NVMe")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("capacity"))
			})

			It("should reject disk specification with empty type", func() {
				_, err := parseDiskFlag(":1000:NVMe")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("type"))
			})
		})

		Describe("parseAcceleratorFlag", func() {
			It("should parse minimal accelerator specification", func() {
				acc, err := parseAcceleratorFlag("GPU:A100")
				Expect(err).NotTo(HaveOccurred())
				Expect(acc.Type).To(Equal("GPU"))
				Expect(acc.Model).To(Equal("A100"))
				Expect(acc.Vendor).To(BeNil())
				Expect(acc.MemoryGb).To(BeNil())
			})

			It("should parse full accelerator specification", func() {
				acc, err := parseAcceleratorFlag("GPU:A100:NVIDIA:80")
				Expect(err).NotTo(HaveOccurred())
				Expect(acc.Type).To(Equal("GPU"))
				Expect(acc.Model).To(Equal("A100"))
				Expect(*acc.Vendor).To(Equal("NVIDIA"))
				Expect(*acc.MemoryGb).To(Equal(int32(80)))
			})

			It("should parse accelerator with vendor but no memory", func() {
				acc, err := parseAcceleratorFlag("FPGA:F1:Intel")
				Expect(err).NotTo(HaveOccurred())
				Expect(acc.Type).To(Equal("FPGA"))
				Expect(acc.Model).To(Equal("F1"))
				Expect(*acc.Vendor).To(Equal("Intel"))
				Expect(acc.MemoryGb).To(BeNil())
			})

			It("should reject accelerator with missing model", func() {
				_, err := parseAcceleratorFlag("GPU")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("format"))
			})

			It("should reject accelerator with invalid memory", func() {
				_, err := parseAcceleratorFlag("GPU:A100:NVIDIA:invalid")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("memory"))
			})
		})

		Describe("parseNetworkPortFlag", func() {
			It("should parse valid network port specification", func() {
				port, err := parseNetworkPortFlag("data-0:fabric:Ethernet:100Gbps")
				Expect(err).NotTo(HaveOccurred())
				Expect(port.Name).To(Equal("data-0"))
				Expect(port.Role).To(Equal("fabric"))
				Expect(port.Type).To(Equal("Ethernet"))
				Expect(port.Speed).To(Equal("100Gbps"))
			})

			It("should reject network port with missing fields", func() {
				_, err := parseNetworkPortFlag("data-0:fabric:Ethernet")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("format"))
			})

			It("should reject network port with empty name", func() {
				_, err := parseNetworkPortFlag(":fabric:Ethernet:100Gbps")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("name"))
			})
		})

		Describe("parseCapabilityFlag", func() {
			It("should parse valid capability key-value pair", func() {
				key, value, err := parseCapabilityFlag("secure-boot=enabled")
				Expect(err).NotTo(HaveOccurred())
				Expect(key).To(Equal("secure-boot"))
				Expect(value).To(Equal("enabled"))
			})

			It("should parse capability with empty value", func() {
				key, value, err := parseCapabilityFlag("tpm-version=")
				Expect(err).NotTo(HaveOccurred())
				Expect(key).To(Equal("tpm-version"))
				Expect(value).To(Equal(""))
			})

			It("should reject capability without equals sign", func() {
				_, _, err := parseCapabilityFlag("secure-boot")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("format"))
			})

			It("should reject capability with empty key", func() {
				_, _, err := parseCapabilityFlag("=enabled")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("key"))
			})
		})

		Describe("parseHostLabelFlag", func() {
			It("should parse valid host label key-value pair", func() {
				key, value, err := parseHostLabelFlag("rack=r1")
				Expect(err).NotTo(HaveOccurred())
				Expect(key).To(Equal("rack"))
				Expect(value).To(Equal("r1"))
			})

			It("should parse host label with hyphenated values", func() {
				key, value, err := parseHostLabelFlag("zone=us-west-1a")
				Expect(err).NotTo(HaveOccurred())
				Expect(key).To(Equal("zone"))
				Expect(value).To(Equal("us-west-1a"))
			})

			It("should reject host label without equals sign", func() {
				_, _, err := parseHostLabelFlag("rack")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("format"))
			})

			It("should reject host label with empty key", func() {
				_, _, err := parseHostLabelFlag("=r1")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("key"))
			})

			It("should reject host label with empty value", func() {
				_, _, err := parseHostLabelFlag("rack=")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("value"))
			})
		})
	})

})
