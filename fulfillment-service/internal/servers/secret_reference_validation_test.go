/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"io"
	"log/slog"

	vaultapi "github.com/hashicorp/vault/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("fetchSecretData", func() {
	var ctx context.Context
	var logger *slog.Logger

	BeforeEach(func() {
		ctx = context.Background()
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	})

	It("returns inline Secret data without touching the store", func() {
		secret := privatev1.Secret_builder{
			Data: map[string][]byte{"public_key": []byte("ssh-ed25519 AAAA")},
		}.Build()

		data, err := fetchSecretData(ctx, logger, nil, secret, "ssh_key")
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("public_key", []byte("ssh-ed25519 AAAA")))
	})

	It("rejects Vault-backed Secrets when no store is configured", func() {
		secret := privatev1.Secret_builder{
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build()

		_, err := fetchSecretData(ctx, logger, nil, secret, "ssh_key")
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
		Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("failed to resolve ssh_key reference"))
	})

	It("maps Vault fetch failures through the store error mapping", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		store := vault.NewMockSecretStore(ctrl)
		store.EXPECT().Fetch(gomock.Any(), "tenant-a", "project-a", "login-key").
			Return(nil, vaultapi.ErrSecretNotFound)
		secret := privatev1.Secret_builder{
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
			Metadata: privatev1.Metadata_builder{
				Name:    "login-key",
				Tenant:  "tenant-a",
				Project: "project-a",
			}.Build(),
		}.Build()

		_, err := fetchSecretData(ctx, logger, store, secret, "ssh_key")
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("secret not found in vault"))
	})

	It("fetches Vault-backed Secret data from the store", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		store := vault.NewMockSecretStore(ctrl)
		store.EXPECT().Fetch(gomock.Any(), "tenant-a", "project-a", "login-key").
			Return(map[string][]byte{"public_key": []byte("ssh-ed25519 AAAA")}, nil)
		secret := privatev1.Secret_builder{
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
			Metadata: privatev1.Metadata_builder{
				Name:    "login-key",
				Tenant:  "tenant-a",
				Project: "project-a",
			}.Build(),
		}.Build()

		data, err := fetchSecretData(ctx, logger, store, secret, "ssh_key")
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(HaveKeyWithValue("public_key", []byte("ssh-ed25519 AAAA")))
	})
})
