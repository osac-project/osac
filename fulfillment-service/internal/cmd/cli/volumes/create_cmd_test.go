/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("volumes create", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := createCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("create"))
		})

		It("should accept no arguments", func() {
			cmd := createCmd()
			Expect(cmd.Args).NotTo(BeNil())
		})
	})

	Context("flag registration", func() {
		It("should register --name flag", func() {
			cmd := createCmd()
			flag := cmd.Flags().Lookup("name")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("NAME"))
		})

		It("should register --storage-tier flag", func() {
			cmd := createCmd()
			flag := cmd.Flags().Lookup("storage-tier")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("TIER"))
		})

		It("should register --size-gib flag", func() {
			cmd := createCmd()
			flag := cmd.Flags().Lookup("size-gib")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("SIZE"))
		})

		It("should register --access-mode flag", func() {
			cmd := createCmd()
			flag := cmd.Flags().Lookup("access-mode")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("MODE"))
		})

		It("should register --project flag", func() {
			cmd := createCmd()
			flag := cmd.Flags().Lookup("project")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("PROJECT"))
		})
	})

	Context("help text", func() {
		It("should have proper short help", func() {
			cmd := createCmd()
			Expect(cmd.Short).To(Equal("Create a volume"))
		})

		It("should have proper long help with examples", func() {
			cmd := createCmd()
			Expect(cmd.Long).To(ContainSubstring("Create a standalone volume."))
			Expect(cmd.Long).To(ContainSubstring("--name"))
			Expect(cmd.Long).To(ContainSubstring("--storage-tier"))
			Expect(cmd.Long).To(ContainSubstring("--size-gib"))
			Expect(cmd.Long).To(ContainSubstring("--access-mode"))
		})
	})

	Context("parseAccessMode", func() {
		It("should parse ReadWriteOnce", func() {
			mode, err := parseAccessMode("ReadWriteOnce")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
		})

		It("should parse RWO abbreviation", func() {
			mode, err := parseAccessMode("RWO")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
		})

		It("should parse ReadOnlyMany", func() {
			mode, err := parseAccessMode("ReadOnlyMany")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY))
		})

		It("should parse ROX abbreviation", func() {
			mode, err := parseAccessMode("ROX")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY))
		})

		It("should parse ReadWriteMany", func() {
			mode, err := parseAccessMode("ReadWriteMany")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY))
		})

		It("should parse RWX abbreviation", func() {
			mode, err := parseAccessMode("RWX")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY))
		})

		It("should parse ReadWriteOncePod", func() {
			mode, err := parseAccessMode("ReadWriteOncePod")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD))
		})

		It("should parse RWOP abbreviation", func() {
			mode, err := parseAccessMode("RWOP")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD))
		})

		It("should be case-insensitive", func() {
			mode, err := parseAccessMode("readwriteonce")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
		})

		It("should reject empty access mode", func() {
			_, err := parseAccessMode("")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("should reject invalid access mode", func() {
			_, err := parseAccessMode("InvalidMode")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid"))
		})
	})
})
