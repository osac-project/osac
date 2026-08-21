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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Hub secrets", Ordered, Label("secrets", "hub"), func() {
	var (
		publicConn           *grpc.ClientConn
		privateSecretsClient privatev1.SecretsClient
		publicSecretsClient  publicv1.SecretsClient
		secretId             string
		secretName           string
		k8sSecretName        string
		k8sSecretData        map[string][]byte
	)

	BeforeAll(func(ctx context.Context) {
		secretName = fmt.Sprintf("test-hub-secret-%s", uuid.New()[24:32])
		k8sSecretName = fmt.Sprintf("hub-test-%s", uuid.New()[24:32])
		k8sSecretData = map[string][]byte{
			"tls.crt": []byte("-----BEGIN CERTIFICATE-----\ntest-cert\n-----END CERTIFICATE-----"),
			"tls.key": []byte("fake-key-data-for-testing"),
		}

		// Create a Kubernetes Secret on the hub cluster for the hub fetcher to read.
		k8sSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hubNamespace,
				Name:      k8sSecretName,
			},
			Data: k8sSecretData,
		}
		err := tool.KubeClient().Create(ctx, k8sSecret)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			_ = tool.KubeClient().Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hubNamespace,
					Name:      k8sSecretName,
				},
			})
		})

		// Private API client (admin) for creating and managing hub secrets.
		privateSecretsClient = privatev1.NewSecretsClient(tool.InternalView().AdminConn())

		// Public API client (adam, engineering tenant) for verifying the user-facing fetch path.
		tokenSource, err := tool.makeKeycloakTokenSource(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		publicConn, err = tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = publicConn.Close()
		})
		publicSecretsClient = publicv1.NewSecretsClient(publicConn)
	})

	It("creates a hub secret via private API", func(ctx context.Context) {
		response, err := privateSecretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
			Object: privatev1.Secret_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   secretName,
					Tenant: "engineering",
				}.Build(),
				Backend: privatev1.SecretBackend_SECRET_BACKEND_HUB,
				Coordinates: map[string]string{
					"hub_id":      hubId,
					"namespace":   hubNamespace,
					"secret_name": k8sSecretName,
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject()).ToNot(BeNil())
		Expect(response.GetObject().GetId()).ToNot(BeEmpty())
		Expect(response.GetObject().GetMetadata().GetName()).To(Equal(secretName))
		Expect(response.GetObject().GetBackend()).To(Equal(privatev1.SecretBackend_SECRET_BACKEND_HUB))
		Expect(response.GetObject().GetData()).To(BeEmpty())
		secretId = response.GetObject().GetId()
	})

	It("gets the hub secret via private API with data from hub", func(ctx context.Context) {
		response, err := privateSecretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
			Id: secretId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetId()).To(Equal(secretId))
		Expect(response.GetObject().GetMetadata().GetName()).To(Equal(secretName))
		Expect(response.GetObject().GetData()).To(Equal(k8sSecretData))
	})

	It("gets the hub secret via public API with data from hub", func(ctx context.Context) {
		response, err := publicSecretsClient.Get(ctx, publicv1.SecretsGetRequest_builder{
			Id: secretId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetId()).To(Equal(secretId))
		Expect(response.GetObject().GetData()).To(Equal(k8sSecretData))
	})

	It("deletes the hub secret via private API", func(ctx context.Context) {
		_, err := privateSecretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{
			Id: secretId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})
})
