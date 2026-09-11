/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package secret

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func formatSecret(s *publicv1.Secret) string {
	var buf bytes.Buffer
	RenderSecret(&buf, s)
	return buf.String()
}

var _ = Describe("Describe Secret", func() {
	Describe("Rendering tests", func() {
		It("should show '-' for optional fields when not set", func() {
			s := publicv1.Secret_builder{
				Id: "sec-001",
			}.Build()

			output := formatSecret(s)
			Expect(output).To(MatchRegexp(`Name:\s+-`))
			Expect(output).To(MatchRegexp(`Project:\s+-`))
			Expect(output).To(MatchRegexp(`Created:\s+-`))
			Expect(output).To(ContainSubstring("Labels:  (none)"))
			Expect(output).To(ContainSubstring("Data:  (none)"))
		})

		It("should display all metadata fields when set", func() {
			s := publicv1.Secret_builder{
				Id: "sec-002",
				Metadata: publicv1.Metadata_builder{
					Name:              "my-tls-cert",
					Project:           "my-project",
					CreationTimestamp: timestamppb.Now(),
					Labels: map[string]string{
						"env":  "production",
						"team": "platform",
					},
				}.Build(),
			}.Build()

			output := formatSecret(s)
			Expect(output).To(ContainSubstring("sec-002"))
			Expect(output).To(ContainSubstring("my-tls-cert"))
			Expect(output).To(ContainSubstring("my-project"))
			Expect(output).To(ContainSubstring("Labels:"))
			Expect(output).To(ContainSubstring("env:"))
			Expect(output).To(ContainSubstring("production"))
			Expect(output).To(ContainSubstring("team:"))
			Expect(output).To(ContainSubstring("platform"))
		})

		It("should show data keys with byte sizes", func() {
			s := publicv1.Secret_builder{
				Id: "sec-003",
				Data: map[string][]byte{
					"tls.crt": []byte("cert-data-here"),
					"tls.key": []byte("key-data"),
				},
			}.Build()

			output := formatSecret(s)
			Expect(output).To(ContainSubstring("Data:"))
			Expect(output).To(ContainSubstring("tls.crt  (14 bytes)"))
			Expect(output).To(ContainSubstring("tls.key  (8 bytes)"))
		})

		It("should sort labels alphabetically", func() {
			s := publicv1.Secret_builder{
				Id: "sec-004",
				Metadata: publicv1.Metadata_builder{
					Labels: map[string]string{
						"z-label": "last",
						"a-label": "first",
					},
				}.Build(),
			}.Build()

			output := formatSecret(s)
			aIdx := bytes.Index([]byte(output), []byte("a-label"))
			zIdx := bytes.Index([]byte(output), []byte("z-label"))
			Expect(aIdx).To(BeNumerically("<", zIdx))
		})

		It("should sort data keys alphabetically", func() {
			s := publicv1.Secret_builder{
				Id: "sec-005",
				Data: map[string][]byte{
					"z-key": []byte("z"),
					"a-key": []byte("a"),
				},
			}.Build()

			output := formatSecret(s)
			aIdx := bytes.Index([]byte(output), []byte("a-key"))
			zIdx := bytes.Index([]byte(output), []byte("z-key"))
			Expect(aIdx).To(BeNumerically("<", zIdx))
		})

		It("should have correct command alias", func() {
			cmd := Cmd()
			Expect(cmd.Aliases).To(ContainElement("secrets"))
		})
	})
})
