/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package diskimage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/fieldutil"
)

var _ = Describe("Create diskimage flag registration", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("diskimage"))
	})

	It("should register --name flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("name")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Name"))
	})

	It("should register --source-type flag with registry default", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("source-type")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal("registry"))
	})

	It("should register --source-ref flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("source-ref")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Reference"))
	})

	It("should register --guest-os-family flag with linux default", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("guest-os-family")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal("linux"))
	})

	It("should register --architecture flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("architecture")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("architecture"))
	})
})

var _ = Describe("Enum parsing", func() {
	Describe("parseEnum for source type", func() {
		It("parses valid source type", func() {
			result, err := fieldutil.ParseEnum("registry", sourceTypeMap, "source-type")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.SourceType_SOURCE_TYPE_REGISTRY))
		})

		It("parses case-insensitively", func() {
			result, err := fieldutil.ParseEnum("Registry", sourceTypeMap, "source-type")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.SourceType_SOURCE_TYPE_REGISTRY))
		})

		It("rejects invalid source type", func() {
			_, err := fieldutil.ParseEnum("s3", sourceTypeMap, "source-type")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid source-type"))
			Expect(err.Error()).To(ContainSubstring("registry"))
		})
	})

	Describe("parseEnum for guest OS family", func() {
		It("parses linux", func() {
			result, err := fieldutil.ParseEnum("linux", guestOsFamilyMap, "guest-os-family")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX))
		})

		It("parses windows", func() {
			result, err := fieldutil.ParseEnum("windows", guestOsFamilyMap, "guest-os-family")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS))
		})

		It("rejects invalid family", func() {
			_, err := fieldutil.ParseEnum("macos", guestOsFamilyMap, "guest-os-family")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid guest-os-family"))
			Expect(err.Error()).To(ContainSubstring("linux"))
			Expect(err.Error()).To(ContainSubstring("windows"))
		})
	})

	Describe("parseEnum for architecture", func() {
		It("parses amd64", func() {
			result, err := fieldutil.ParseEnum("amd64", architectureMap, "architecture")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.Architecture_ARCHITECTURE_AMD64))
		})

		It("parses arm64", func() {
			result, err := fieldutil.ParseEnum("arm64", architectureMap, "architecture")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.Architecture_ARCHITECTURE_ARM64))
		})

		It("parses s390x", func() {
			result, err := fieldutil.ParseEnum("s390x", architectureMap, "architecture")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(publicv1.Architecture_ARCHITECTURE_S390X))
		})

		It("rejects invalid architecture", func() {
			_, err := fieldutil.ParseEnum("ppc64le", architectureMap, "architecture")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid architecture"))
		})
	})
})
