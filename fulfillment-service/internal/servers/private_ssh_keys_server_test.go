/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const validSshKeyForTest = testSSHPublicKey

var _ = Describe("Private SSH keys server", func() {
	Describe("Builder", func() {
		It("builds with the required dependencies", func() {
			server, err := NewPrivateSshKeysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("requires a logger", func() {
			server, err := NewPrivateSshKeysServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("requires tenancy logic", func() {
			server, err := NewPrivateSshKeysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Create validation", func() {
		var server *PrivateSshKeysServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateSshKeysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createRequest := func(name, publicKey, project string) *privatev1.SshKeysCreateRequest {
			return privatev1.SshKeysCreateRequest_builder{
				Object: privatev1.SshKey_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    name,
						Project: project,
					}.Build(),
					Spec: privatev1.SshKeySpec_builder{
						PublicKey: publicKey,
					}.Build(),
				}.Build(),
			}.Build()
		}

		It("rejects a missing object", func() {
			_, err := server.Create(context.Background(), &privatev1.SshKeysCreateRequest{})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("ssh key is mandatory"))
		})

		It("rejects an empty name", func() {
			_, err := server.Create(ctx, createRequest("", validSshKeyForTest, ""))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("metadata.name"))
		})

		It("rejects a project because SSH keys are tenant scoped", func() {
			_, err := server.Create(ctx, createRequest("my-key", validSshKeyForTest, "project-a"))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("metadata.project"))
		})

		It("rejects malformed OpenSSH public key material", func() {
			_, err := server.Create(ctx, createRequest("my-key", "not-a-key", ""))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("spec.public_key"))
		})

		It("rejects trailing content after an OpenSSH public key", func() {
			invalidKey := validSshKeyForTest
			invalidKey += "\ntrailing"
			_, err := server.Create(ctx, createRequest("my-key", invalidKey, ""))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("spec.public_key"))
		})
	})

	It("returns Unimplemented for private Update", func() {
		server, err := NewPrivateSshKeysServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.Update(ctx, &privatev1.SshKeysUpdateRequest{})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
})
