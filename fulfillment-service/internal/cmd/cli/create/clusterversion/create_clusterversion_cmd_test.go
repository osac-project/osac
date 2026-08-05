/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversion

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Create clusterversion flag registration", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("clusterversion"))
	})

	It("should register --name flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("name")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Name"))
	})

	It("should register --version flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("version")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("SemVer"))
	})

	It("should register --image flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("image")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("OCI"))
	})

	It("should register --enabled flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("enabled")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --default flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("default")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --state flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("state")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("ACTIVE"))
	})

})

var _ = Describe("State parsing", func() {
	It("should parse ACTIVE", func() {
		state, err := parseState("ACTIVE")
		Expect(err).NotTo(HaveOccurred())
		Expect(state.String()).To(ContainSubstring("ACTIVE"))
	})

	It("should parse case-insensitive input", func() {
		state, err := parseState("deprecated")
		Expect(err).NotTo(HaveOccurred())
		Expect(state.String()).To(ContainSubstring("DEPRECATED"))
	})

	It("should parse OBSOLETE", func() {
		state, err := parseState("Obsolete")
		Expect(err).NotTo(HaveOccurred())
		Expect(state.String()).To(ContainSubstring("OBSOLETE"))
	})

	It("should return UNSPECIFIED for empty string", func() {
		state, err := parseState("")
		Expect(err).NotTo(HaveOccurred())
		Expect(state.String()).To(ContainSubstring("UNSPECIFIED"))
	})

	It("should reject invalid state", func() {
		_, err := parseState("INVALID")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid state"))
	})
})
