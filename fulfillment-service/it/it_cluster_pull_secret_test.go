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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// dockerConfigJSON is a representative (fake) container registry pull secret payload.
const dockerConfigJSON = `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`

var _ = Describe("Cluster pull_secret_secret", Label("secrets", "cluster"), func() {
	var (
		ctx             context.Context
		clustersClient  publicv1.ClustersClient
		secretsClient   publicv1.SecretsClient
		hostTypesClient privatev1.HostTypesClient
		templatesClient privatev1.ClusterTemplatesClient
		hostTypeId      string

		makeAny = func(value proto.Message) *anypb.Any {
			result, err := anypb.New(value)
			Expect(err).ToNot(HaveOccurred())
			return result
		}
	)

	// createSecret creates a Vault-backed secret owned by the same tenant that creates the
	// clusters, so that both the server-side reference validation (create path) and the cluster
	// reconciler (controller path) can resolve it.
	createSecret := func(ctx context.Context, data map[string][]byte) (id, name string) {
		name = fmt.Sprintf("pull-secret-%s", uuid.New()[24:32])
		response, err := secretsClient.Create(ctx, publicv1.SecretsCreateRequest_builder{
			Object: publicv1.Secret_builder{
				Type: publicv1.SecretType_SECRET_TYPE_PULL_SECRET,
				Metadata: publicv1.Metadata_builder{
					Name: name,
				}.Build(),
				Data: data,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id = response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = secretsClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: id}.Build())
		})
		return id, name
	}

	// createOpaqueSecret creates a valid secret which cannot be used as a cluster pull secret.
	createOpaqueSecret := func(ctx context.Context) (id, name string) {
		name = fmt.Sprintf("opaque-secret-%s", uuid.New()[24:32])
		response, err := secretsClient.Create(ctx, publicv1.SecretsCreateRequest_builder{
			Object: publicv1.Secret_builder{
				Type: publicv1.SecretType_SECRET_TYPE_OPAQUE,
				Metadata: publicv1.Metadata_builder{
					Name: name,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id = response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = secretsClient.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: id}.Build())
		})
		return id, name
	}

	// createTemplate creates a cluster template with a single required node set. When defaults is
	// non-nil it is attached as the template's spec_defaults.
	createTemplate := func(ctx context.Context, defaults *privatev1.ClusterTemplateSpecDefaults) string {
		templateId := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateId,
				Title:       "Pull secret template",
				Description: "Pull secret template.",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Parameters: []*privatev1.ClusterTemplateParameterDefinition{
					privatev1.ClusterTemplateParameterDefinition_builder{
						Name:        "my",
						Type:        "type.googleapis.com/google.protobuf.StringValue",
						Title:       "My required parameter",
						Description: "My required parameter.",
						Required:    true,
					}.Build(),
				},
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my_node_set": privatev1.ClusterTemplateNodeSet_builder{
						HostType: privatev1.HostTypeReference_builder{Id: hostTypeId}.Build(),
						Size:     3,
					}.Build(),
				},
				SpecDefaults: defaults,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			_, _ = templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		})
		return templateId
	}

	// waitForClusterOrder waits for the ClusterOrder Kubernetes object created for the given
	// cluster and returns it.
	waitForClusterOrder := func(ctx context.Context, clusterId string) *osacv1alpha1.ClusterOrder {
		kubeClient := tool.KubeClient()
		clusterOrderList := &osacv1alpha1.ClusterOrderList{}
		var kubeObject *osacv1alpha1.ClusterOrder
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, clusterOrderList, crclient.MatchingLabels{
					labels.ClusterOrderUuid: clusterId,
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderList.Items).To(HaveLen(1))
				kubeObject = &clusterOrderList.Items[0]
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		return kubeObject
	}

	BeforeEach(func() {
		ctx = context.Background()

		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		secretsClient = publicv1.NewSecretsClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		hostTypeId = fmt.Sprintf("my_host_type_%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: hostTypeId,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ht-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("Resolves pull_secret_secret by id into the ClusterOrder pull secret", func() {
		secretId, _ := createSecret(ctx, map[string][]byte{".dockerconfigjson": []byte(dockerConfigJSON)})
		templateId := createTemplate(ctx, nil)

		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					PullSecretSecret: publicv1.SecretLocalReference_builder{Id: secretId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: clusterId}.Build())
		})

		kubeObject := waitForClusterOrder(ctx, clusterId)
		Expect(kubeObject.Spec.PullSecret).To(Equal(dockerConfigJSON))
	})

	It("Resolves pull_secret_secret by name into the ClusterOrder pull secret", func() {
		_, secretName := createSecret(ctx, map[string][]byte{".dockerconfigjson": []byte(dockerConfigJSON)})
		templateId := createTemplate(ctx, nil)

		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					PullSecretSecret: publicv1.SecretLocalReference_builder{Name: secretName}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: clusterId}.Build())
		})

		kubeObject := waitForClusterOrder(ctx, clusterId)
		Expect(kubeObject.Spec.PullSecret).To(Equal(dockerConfigJSON))
	})

	It("Rejects a cluster that references a nonexistent secret", func() {
		templateId := createTemplate(ctx, nil)

		_, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					PullSecretSecret: publicv1.SecretLocalReference_builder{
						Id: uuid.New(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects pull_secret_secret with an incompatible secret type", func() {
		// Typed-secret validation prevents malformed pull secrets from being created. Use a
		// valid opaque secret to exercise the cluster API's reference type validation.
		secretId, _ := createOpaqueSecret(ctx)
		templateId := createTemplate(ctx, nil)

		_, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					PullSecretSecret: publicv1.SecretLocalReference_builder{Id: secretId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("SECRET_TYPE_OPAQUE"))
		Expect(status.Message()).To(ContainSubstring("SECRET_TYPE_PULL_SECRET"))
	})

	It("Propagates a template default pull_secret_secret into the ClusterOrder", func() {
		secretId, _ := createSecret(ctx, map[string][]byte{".dockerconfigjson": []byte(dockerConfigJSON)})
		templateId := createTemplate(ctx, privatev1.ClusterTemplateSpecDefaults_builder{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Id: secretId}.Build(),
		}.Build())

		// Create a cluster from the template without any pull secret of its own.
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: clusterId}.Build())
		})

		kubeObject := waitForClusterOrder(ctx, clusterId)
		Expect(kubeObject.Spec.PullSecret).To(Equal(dockerConfigJSON))
	})

	It("Lets a user pull_secret_secret override a template default", func() {
		secretId, _ := createSecret(ctx, map[string][]byte{".dockerconfigjson": []byte(dockerConfigJSON)})
		templateId := createTemplate(ctx, privatev1.ClusterTemplateSpecDefaults_builder{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Id: secretId}.Build(),
		}.Build())

		const overridePullSecret = `{"auths":{"override.example.com":{"auth":"aW5saW5lOnZhbHVl"}}}`
		overrideSecretId, _ := createSecret(ctx, map[string][]byte{
			".dockerconfigjson": []byte(overridePullSecret),
		})
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					PullSecretSecret: publicv1.SecretLocalReference_builder{Id: overrideSecretId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: clusterId}.Build())
		})

		kubeObject := waitForClusterOrder(ctx, clusterId)
		Expect(kubeObject.Spec.PullSecret).To(Equal(overridePullSecret))
	})
})
