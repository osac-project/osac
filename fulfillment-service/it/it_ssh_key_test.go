/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("SshKey lifecycle", func() {
	var client publicv1.SshKeysClient

	BeforeEach(func() {
		client = publicv1.NewSshKeysClient(tool.ExternalView().UserConn())
	})

	It("creates, lists, gets, and deletes a tenant-scoped key", func(ctx context.Context) {
		name := "ssh-key-" + uuid.New()[24:]
		createResponse, err := client.Create(ctx, publicv1.SshKeysCreateRequest_builder{
			Object: publicv1.SshKey_builder{
				Metadata: publicv1.Metadata_builder{Name: name}.Build(),
				Spec:     publicv1.SshKeySpec_builder{PublicKey: bmiTestSSHPublicKey}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		object := createResponse.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetMetadata().GetTenant()).ToNot(BeEmpty())
		Expect(object.GetMetadata().GetProject()).To(BeEmpty())
		Expect(object.GetSpec().GetPublicKey()).To(Equal(bmiTestSSHPublicKey))

		id := object.GetId()
		getResponse, err := client.Get(ctx, publicv1.SshKeysGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal(name))

		listResponse, err := client.List(ctx, publicv1.SshKeysListRequest_builder{
			Filter: new("this.metadata.name == '" + name + "'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetId()).To(Equal(id))

		_, err = client.Delete(ctx, publicv1.SshKeysDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = client.Get(ctx, publicv1.SshKeysGetRequest_builder{Id: id}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))

		_, err = client.Create(ctx, publicv1.SshKeysCreateRequest_builder{
			Object: publicv1.SshKey_builder{
				Metadata: publicv1.Metadata_builder{Name: name}.Build(),
				Spec:     publicv1.SshKeySpec_builder{PublicKey: bmiTestSSHPublicKey}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
	})
})
