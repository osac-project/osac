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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// dockerConfigJSON is a representative (fake) container registry pull secret payload.
const dockerConfigJSON = `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`

func clusterPullSecretDefaultsFixture(ctx context.Context) *privatev1.ClusterTemplateSpecDefaults {
	GinkgoHelper()
	reference := clusterPullSecretReferenceFixture(ctx, tool.ExternalView().UserConn(), "")
	return privatev1.ClusterTemplateSpecDefaults_builder{
		PullSecretSecret: privatev1.SecretLocalReference_builder{Id: reference.GetId()}.Build(),
	}.Build()
}

func clusterPullSecretSharedDefaultsFixture(ctx context.Context) *privatev1.ClusterTemplateSpecDefaults {
	GinkgoHelper()
	client := privatev1.NewSecretsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: "fixture-shared-pull-secret-" + uuid.New(), Tenant: "shared"}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
			Data:     map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerConfigJSON)},
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	secretID := response.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: secretID}.Build())
		Expect(err).ToNot(HaveOccurred())
	})
	return privatev1.ClusterTemplateSpecDefaults_builder{
		PullSecretSecret: privatev1.SecretLocalReference_builder{Id: secretID}.Build(),
	}.Build()
}

func clusterPullSecretReferenceFixture(ctx context.Context, conn grpc.ClientConnInterface, tenant string) *publicv1.SecretLocalReference {
	GinkgoHelper()
	client := publicv1.NewSecretsClient(conn)
	response, err := client.Create(ctx, publicv1.SecretsCreateRequest_builder{
		Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{Name: "fixture-pull-secret-" + uuid.New(), Tenant: tenant}.Build(),
			Type:     publicv1.SecretType_SECRET_TYPE_PULL_SECRET,
			Data:     map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerConfigJSON)},
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	secretID := response.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := privatev1.NewSecretsClient(tool.InternalView().AdminConn()).Delete(ctx,
			privatev1.SecretsDeleteRequest_builder{Id: secretID}.Build())
		Expect(err).ToNot(HaveOccurred())
	})
	return publicv1.SecretLocalReference_builder{Id: secretID}.Build()
}

func containsPullSecretPayload(encoded []byte, payload string) bool {
	escaped, err := json.Marshal(payload)
	return err != nil || bytes.Contains(encoded, []byte(payload)) || bytes.Contains(encoded, escaped) ||
		bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString([]byte(payload))))
}

var _ = Describe("Cluster pull_secret_secret", Label("secrets", "cluster"), func() {
	var (
		ctx                 context.Context
		clustersClient      publicv1.ClustersClient
		secretsClient       publicv1.SecretsClient
		instanceTypesClient privatev1.BareMetalInstanceTypesClient
		templatesClient     privatev1.ClusterTemplatesClient
		bmitName            string

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

	// createTemplate creates a cluster template with provisioning defaults but no hardware selection.
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
		GinkgoHelper()
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
				g.Expect(kubeObject.Spec.PullSecretRevision).ToNot(BeEmpty())
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		return kubeObject
	}

	assertProjectedPullSecret := func(ctx context.Context, clusterId, expectedPayload string) {
		GinkgoHelper()
		order := waitForClusterOrder(ctx, clusterId)
		hasInlineCredentials := order.Spec.PullSecret != ""
		Expect(hasInlineCredentials).To(BeFalse(), "ClusterOrder must not store inline credentials")
		Expect(order.Spec.PullSecretRef).ToNot(BeNil())
		Expect(order.Spec.PullSecretRef.Name).To(Equal("cluster-" + clusterId + "-pull-secret"))
		Expect(order.Spec.PullSecretRevision).ToNot(BeEmpty())
		encoded, err := json.Marshal(order)
		Expect(err).ToNot(HaveOccurred())
		for _, payload := range []string{dockerConfigJSON, expectedPayload} {
			Expect(containsPullSecretPayload(encoded, payload)).To(BeFalse(), "ClusterOrder must not contain credential payloads")
		}
		projected := &corev1.Secret{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{
			Namespace: order.Namespace,
			Name:      order.Spec.PullSecretRef.Name,
		}, projected)).To(Succeed())
		Expect(projected.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
		dataKeys := make([]string, 0, len(projected.Data))
		for key := range projected.Data {
			dataKeys = append(dataKeys, key)
		}
		Expect(dataKeys).To(ConsistOf(corev1.DockerConfigJsonKey))
		Expect(bytes.Equal(projected.Data[corev1.DockerConfigJsonKey], []byte(expectedPayload))).To(BeTrue(), "projected synthetic pull-secret content mismatch")
		Expect(projected.Annotations["osac.openshift.io/pull-secret-revision"]).To(Equal(order.Spec.PullSecretRevision))
		for _, key := range []string{"osac.openshift.io/tenant", "osac.openshift.io/project"} {
			Expect(projected.Annotations[key]).To(Equal(order.Annotations[key]))
		}
		Expect(metav1.IsControlledBy(projected, order)).To(BeTrue())
	}

	BeforeEach(func() {
		ctx = context.Background()
		getOrCreateTenantDefaultNetworkClassFixture(ctx)

		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		secretsClient = publicv1.NewSecretsClient(tool.ExternalView().UserConn())
		instanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		bmitName = fmt.Sprintf("test-bmit-%s", uuid.New()[24:32])
		_, err := instanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Id: bmitName,
				Metadata: privatev1.Metadata_builder{
					Name: bmitName,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "amd64", ThreadsPerCore: 2}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 128}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name:  "eth0",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "10Gbps",
							}.Build(),
						},
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{"hardware.profile": "compute"},
					}.Build(),
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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

		assertProjectedPullSecret(ctx, clusterId, dockerConfigJSON)
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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

		assertProjectedPullSecret(ctx, clusterId, dockerConfigJSON)
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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

		assertProjectedPullSecret(ctx, clusterId, dockerConfigJSON)
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
					NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": publicv1.ClusterNodeSet_builder{
						Size: proto.Int32(2), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmitName, Shared: true}.Build(),
					}.Build()},
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

		assertProjectedPullSecret(ctx, clusterId, overridePullSecret)
	})
})
