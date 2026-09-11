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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public secrets", Ordered, Label("secrets", "vault"), func() {
	Describe("CRUD lifecycle", Ordered, func() {
		var (
			conn          *grpc.ClientConn
			secretsClient publicv1.SecretsClient
			secretId      string
			secretName    string
			initialData   map[string][]byte
			updatedData   map[string][]byte
		)

		BeforeAll(func(ctx context.Context) {
			secretName = fmt.Sprintf("test-secret-%s", uuid.New()[24:32])
			initialData = map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("s3cr3t"),
			}
			updatedData = map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("n3w-s3cr3t"),
			}

			tokenSource, err := tool.makeKeycloakTokenSource(ctx, "adam", usersPassword)
			Expect(err).ToNot(HaveOccurred())
			conn, err = tool.makeGrpcConn(externalServiceAddr, tokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = conn.Close()
			})
			secretsClient = publicv1.NewSecretsClient(conn)
		})

		It("creates a secret", func(ctx context.Context) {
			response, err := secretsClient.Create(ctx, publicv1.SecretsCreateRequest_builder{
				Object: publicv1.Secret_builder{
					Metadata: publicv1.Metadata_builder{
						Name: secretName,
					}.Build(),
					Data: initialData,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal(secretName))
			Expect(response.GetObject().GetData()).To(BeEmpty())
			secretId = response.GetObject().GetId()
		})

		It("gets the secret with data from vault", func(ctx context.Context) {
			response, err := secretsClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: secretId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).To(Equal(secretId))
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal(secretName))
			Expect(response.GetObject().GetData()).To(Equal(initialData))
		})

		It("lists secrets and the created secret appears", func(ctx context.Context) {
			response, err := secretsClient.List(ctx, publicv1.SecretsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(BeNumerically(">=", 1))

			var found bool
			for _, item := range response.GetItems() {
				if item.GetId() == secretId {
					found = true
					Expect(item.GetMetadata().GetName()).To(Equal(secretName))
					Expect(item.GetData()).To(BeEmpty())
					break
				}
			}
			Expect(found).To(BeTrue(), "created secret not found in list")
		})

		It("updates the secret data", func(ctx context.Context) {
			response, err := secretsClient.Update(ctx, publicv1.SecretsUpdateRequest_builder{
				Object: publicv1.Secret_builder{
					Id: secretId,
					Metadata: publicv1.Metadata_builder{
						Name: secretName,
					}.Build(),
					Data: updatedData,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetData()).To(BeEmpty())
		})

		It("gets the secret after update and verifies new data", func(ctx context.Context) {
			response, err := secretsClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: secretId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetData()).To(Equal(updatedData))
		})

		It("deletes the secret", func(ctx context.Context) {
			_, err := secretsClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
				Id: secretId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns NotFound after deletion", func(ctx context.Context) {
			_, err := secretsClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: secretId,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})
	})

	Describe("Tenant isolation", Ordered, func() {
		var (
			adamConn     *grpc.ClientConn
			benConn      *grpc.ClientConn
			adamClient   publicv1.SecretsClient
			benClient    publicv1.SecretsClient
			adamSecretId string
			benSecretId  string
			adamData     map[string][]byte
			benData      map[string][]byte
		)

		BeforeAll(func(ctx context.Context) {
			adamData = map[string][]byte{"key": []byte("adam-value")}
			benData = map[string][]byte{"key": []byte("ben-value")}

			adamTokenSource, err := tool.makeKeycloakTokenSource(ctx, "adam", usersPassword)
			Expect(err).ToNot(HaveOccurred())
			adamConn, err = tool.makeGrpcConn(externalServiceAddr, adamTokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = adamConn.Close() })
			adamClient = publicv1.NewSecretsClient(adamConn)

			benTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
			Expect(err).ToNot(HaveOccurred())
			benConn, err = tool.makeGrpcConn(externalServiceAddr, benTokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = benConn.Close() })
			benClient = publicv1.NewSecretsClient(benConn)

			adamResp, err := adamClient.Create(ctx, publicv1.SecretsCreateRequest_builder{
				Object: publicv1.Secret_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("isolation-adam-%s", uuid.New()[24:32]),
					}.Build(),
					Data: adamData,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			adamSecretId = adamResp.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, _ = adamClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
					Id: adamSecretId,
				}.Build())
			})

			benResp, err := benClient.Create(ctx, publicv1.SecretsCreateRequest_builder{
				Object: publicv1.Secret_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("isolation-ben-%s", uuid.New()[24:32]),
					}.Build(),
					Data: benData,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			benSecretId = benResp.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, _ = benClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
					Id: benSecretId,
				}.Build())
			})
		})

		It("each user lists only their own secrets", func(ctx context.Context) {
			adamList, err := adamClient.List(ctx, publicv1.SecretsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			adamIds := make([]string, len(adamList.GetItems()))
			for i, item := range adamList.GetItems() {
				adamIds[i] = item.GetId()
			}
			Expect(adamIds).To(ContainElement(adamSecretId))
			Expect(adamIds).ToNot(ContainElement(benSecretId))

			benList, err := benClient.List(ctx, publicv1.SecretsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			benIds := make([]string, len(benList.GetItems()))
			for i, item := range benList.GetItems() {
				benIds[i] = item.GetId()
			}
			Expect(benIds).To(ContainElement(benSecretId))
			Expect(benIds).ToNot(ContainElement(adamSecretId))
		})

		It("each user can get their own secret", func(ctx context.Context) {
			adamResp, err := adamClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: adamSecretId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(adamResp.GetObject().GetData()).To(Equal(adamData))

			benResp, err := benClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: benSecretId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(benResp.GetObject().GetData()).To(Equal(benData))
		})

		It("cross-tenant get returns NotFound", func(ctx context.Context) {
			_, err := adamClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: benSecretId,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))

			_, err = benClient.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: adamSecretId,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok = grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("cross-tenant delete returns NotFound", func(ctx context.Context) {
			_, err := adamClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
				Id: benSecretId,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))

			_, err = benClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
				Id: adamSecretId,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok = grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})
	})
})
