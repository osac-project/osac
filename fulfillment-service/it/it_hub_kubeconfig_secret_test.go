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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

// sampleHubKubeconfig is a syntactically-plausible kubeconfig payload. The Hubs server validation
// only checks that the referenced Secret exists (it never reads the data), so the exact content is
// immaterial to these create-time tests.
const sampleHubKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: hub
  cluster:
    server: https://kubernetes.default.svc
contexts:
- name: hub
  context:
    cluster: hub
    user: hub
current-context: hub
users:
- name: hub
  user:
    token: fake-token
`

var _ = Describe("Hub kubeconfig_secret", Label("secrets", "hub"), func() {
	var (
		ctx           context.Context
		hubsClient    privatev1.HubsClient
		secretsClient privatev1.SecretsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		hubsClient = privatev1.NewHubsClient(tool.InternalView().AdminConn())
		secretsClient = privatev1.NewSecretsClient(tool.InternalView().AdminConn())
	})

	// createKubeconfigSecret creates a Vault-backed secret carrying a kubeconfig payload and returns
	// its id and name.
	createKubeconfigSecret := func(ctx context.Context, data map[string][]byte) (id, name string) {
		name = fmt.Sprintf("hub-kubeconfig-%s", uuid.New()[24:32])
		response, err := secretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
			Object: privatev1.Secret_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: auth.SharedTenant,
				}.Build(),
				Data: data,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id = response.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: id}.Build())
		})
		return id, name
	}

	// createHub creates a hub with a unique id/namespace using the given spec and registers cleanup.
	createHub := func(ctx context.Context, spec *privatev1.HubSpec) (*privatev1.Hub, error) {
		hubId := fmt.Sprintf("test-hub-%s", uuid.New())
		response, err := hubsClient.Create(ctx, privatev1.HubsCreateRequest_builder{
			Object: privatev1.Hub_builder{
				Id: hubId,
				Metadata: privatev1.Metadata_builder{
					Name: hubId,
				}.Build(),
				Spec: spec,
			}.Build(),
		}.Build())
		if err == nil {
			DeferCleanup(func() {
				_, _ = hubsClient.Delete(ctx, privatev1.HubsDeleteRequest_builder{Id: hubId}.Build())
			})
		}
		return response.GetObject(), err
	}

	It("Accepts a hub referencing kubeconfig_secret by id and writes back the resolved ref", func() {
		secretId, secretName := createKubeconfigSecret(ctx, map[string][]byte{
			"kubeconfig": []byte(sampleHubKubeconfig),
		})

		hub, err := createHub(ctx, privatev1.HubSpec_builder{
			Namespace:        fmt.Sprintf("test-hub-ns-%s", uuid.New()[24:32]),
			KubeconfigSecret: privatev1.SecretLocalReference_builder{Id: secretId}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// The server resolves the reference and writes back both id and name.
		ref := hub.GetSpec().GetKubeconfigSecret()
		Expect(ref).ToNot(BeNil())
		Expect(ref.GetId()).To(Equal(secretId))
		Expect(ref.GetName()).To(Equal(secretName))
	})

	It("Accepts a hub referencing kubeconfig_secret by name and resolves the id", func() {
		secretId, secretName := createKubeconfigSecret(ctx, map[string][]byte{
			"kubeconfig": []byte(sampleHubKubeconfig),
		})

		hub, err := createHub(ctx, privatev1.HubSpec_builder{
			Namespace:        fmt.Sprintf("test-hub-ns-%s", uuid.New()[24:32]),
			KubeconfigSecret: privatev1.SecretLocalReference_builder{Name: secretName}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		ref := hub.GetSpec().GetKubeconfigSecret()
		Expect(ref).ToNot(BeNil())
		Expect(ref.GetId()).To(Equal(secretId))
		Expect(ref.GetName()).To(Equal(secretName))
	})

	It("Rejects a hub that sets both kubeconfig and kubeconfig_secret", func() {
		secretId, _ := createKubeconfigSecret(ctx, map[string][]byte{
			"kubeconfig": []byte(sampleHubKubeconfig),
		})

		_, err := createHub(ctx, privatev1.HubSpec_builder{
			Namespace:        fmt.Sprintf("test-hub-ns-%s", uuid.New()[24:32]),
			Kubeconfig:       []byte(sampleHubKubeconfig),
			KubeconfigSecret: privatev1.SecretLocalReference_builder{Id: secretId}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects a hub referencing a nonexistent secret", func() {
		_, err := createHub(ctx, privatev1.HubSpec_builder{
			Namespace:        fmt.Sprintf("test-hub-ns-%s", uuid.New()[24:32]),
			KubeconfigSecret: privatev1.SecretLocalReference_builder{Id: uuid.New()}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects updating kubeconfig while kubeconfig_secret is set", func() {
		secretId, _ := createKubeconfigSecret(ctx, map[string][]byte{
			"kubeconfig": []byte(sampleHubKubeconfig),
		})

		hubId := fmt.Sprintf("test-hub-%s", uuid.New())
		_, err := hubsClient.Create(ctx, privatev1.HubsCreateRequest_builder{
			Object: privatev1.Hub_builder{
				Id: hubId,
				Metadata: privatev1.Metadata_builder{
					Name: hubId,
				}.Build(),
				Spec: privatev1.HubSpec_builder{
					Namespace:        fmt.Sprintf("test-hub-ns-%s", uuid.New()[24:32]),
					KubeconfigSecret: privatev1.SecretLocalReference_builder{Id: secretId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = hubsClient.Delete(ctx, privatev1.HubsDeleteRequest_builder{Id: hubId}.Build())
		})

		// Setting the inline kubeconfig via the mask while the stored hub still carries a
		// kubeconfig_secret (not cleared in the same request) is a mutual-exclusion conflict.
		_, err = hubsClient.Update(ctx, privatev1.HubsUpdateRequest_builder{
			Object: privatev1.Hub_builder{
				Id: hubId,
				Spec: privatev1.HubSpec_builder{
					Kubeconfig: []byte(sampleHubKubeconfig),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.kubeconfig"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})
})
