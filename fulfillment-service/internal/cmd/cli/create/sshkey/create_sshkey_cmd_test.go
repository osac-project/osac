/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package sshkey

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Create SSH key command", func() {
	It("registers the public SSH key command and flags", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("sshkey [FLAG...]"))
		Expect(cmd.Aliases).To(ContainElement(string(proto.MessageName((*publicv1.SshKey)(nil)))))
		Expect(cmd.Flags().Lookup("name").Shorthand).To(Equal("n"))
		Expect(cmd.Flags().Lookup("public-key")).ToNot(BeNil())
		Expect(cmd.Flags().Lookup("public-key-file")).ToNot(BeNil())
	})

	It("requires a name", func() {
		cmd := Cmd()
		cmd.SetArgs([]string{"--public-key", "key-material"})
		Expect(cmd.Execute()).To(MatchError(ContainSubstring("required flag(s) \"name\" not set")))
	})

	It("requires exactly one public key source", func() {
		cmd := Cmd()
		cmd.SetArgs([]string{"--name", "my-key"})
		Expect(cmd.Execute()).To(MatchError(ContainSubstring("exactly one of --public-key or --public-key-file is required")))
	})

	It("rejects both public key sources", func() {
		cmd := Cmd()
		cmd.SetArgs([]string{
			"--name", "my-key",
			"--public-key", "key-material",
			"--public-key-file", "key-file",
		})
		Expect(cmd.Execute()).To(MatchError(ContainSubstring("if any flags in the group")))
	})

	Describe("resolvePublicKey", func() {
		It("returns direct public key material", func() {
			result, err := resolvePublicKey("  key-material  ", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("key-material"))
		})

		It("reads and trims a public key file", func() {
			path := filepath.Join(GinkgoT().TempDir(), "id_test.pub")
			Expect(os.WriteFile(path, []byte("\n  key-material  \n"), 0600)).To(Succeed())

			result, err := resolvePublicKey("", path)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("key-material"))
		})

		It("expands a leading tilde in a public key file path", func() {
			home := GinkgoT().TempDir()
			previousHome, hadHome := os.LookupEnv("HOME")
			Expect(os.Setenv("HOME", home)).To(Succeed())
			DeferCleanup(func() {
				if hadHome {
					Expect(os.Setenv("HOME", previousHome)).To(Succeed())
				} else {
					Expect(os.Unsetenv("HOME")).To(Succeed())
				}
			})

			Expect(os.WriteFile(filepath.Join(home, "id_test.pub"), []byte("key-material"), 0600)).To(Succeed())
			result, err := resolvePublicKey("", "~/id_test.pub")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("key-material"))
		})

		It("rejects an empty source", func() {
			_, err := resolvePublicKey("", "")
			Expect(err).To(MatchError("exactly one of --public-key or --public-key-file is required"))
		})

		It("reports file read errors", func() {
			_, err := resolvePublicKey("", filepath.Join(GinkgoT().TempDir(), "missing.pub"))
			Expect(err).To(MatchError(ContainSubstring("failed to read public key file")))
		})
	})

	It("builds a public client request object", func() {
		object, err := buildSshKey("my-key", "key-material", "my-tenant")
		Expect(err).ToNot(HaveOccurred())
		Expect(object.GetMetadata().GetName()).To(Equal("my-key"))
		Expect(object.GetMetadata().GetTenant()).To(Equal("my-tenant"))
		Expect(object.GetSpec().GetPublicKey()).To(Equal("key-material"))
	})
})
