/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package onboarding

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kafkatopics"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/strimzi"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The environment for the tenant Kafka topic tests. It runs the reconciler against a real API server, because the
// fake client does not apply custom resource definitions.
var topicsEnv struct {
	environment *envtest.Environment
	client      clnt.Client
}

var _ = Describe("Tenant Kafka topics", Ordered, func() {
	const (
		tenantID       = "tenant-123"
		tenantName     = "my-tenant"
		kafkaHubID     = "hub"
		kafkaNamespace = "osac-kafka"
		clusterName    = "osac-kafka"
		retentionMS    = int64(604800000)
	)

	var ctx context.Context

	BeforeAll(func() {
		if os.Getenv("KUBEBUILDER_ASSETS") == "" {
			Skip("KUBEBUILDER_ASSETS is not set; set it with setup-envtest to run this suite")
		}

		var err error
		topicsEnv.environment = &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("testdata")},
			ErrorIfCRDPathMissing: true,
		}
		config, err := topicsEnv.environment.Start()
		Expect(err).ToNot(HaveOccurred())

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(strimzi.AddToScheme(scheme)).To(Succeed())
		topicsEnv.client, err = clnt.New(config, clnt.Options{Scheme: scheme})
		Expect(err).ToNot(HaveOccurred())

		Expect(topicsEnv.client.Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: kafkaNamespace},
		})).To(Succeed())

		ctx = context.Background()
	})

	AfterAll(func() {
		if topicsEnv.environment != nil {
			Expect(topicsEnv.environment.Stop()).To(Succeed())
		}
	})

	// newTopicsFunction builds a reconciler for an installation with no workload hubs, so the only Kubernetes
	// objects reconciliation touches are the tenant's topics on the Kafka hub.
	newTopicsFunction := func(ctrl *gomock.Controller) (*function, *controllers.MockHubCache) {
		hubCache := controllers.NewMockHubCache(ctrl)
		hubs := controllers.NewMockHubsClient(ctrl)
		hubs.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.HubsListResponse_builder{}.Build(), nil).
			AnyTimes()
		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			hubsClient:     hubs,
			tenantsClient:  NewMockTenantsClient(ctrl),
			projectsClient: NewMockProjectsClient(ctrl),
			maskCalculator: masks.NewCalculator().Build(),
			topics: TopicsConfig{
				HubID:       kafkaHubID,
				Namespace:   kafkaNamespace,
				ClusterName: clusterName,
				RetentionMS: retentionMS,
			},
		}
		return f, hubCache
	}

	// reconcileTenant runs the update path of the reconciler for a tenant that already has its finalizers.
	reconcileTenant := func(f *function) error {
		tenant := privatev1.Tenant_builder{
			Id: tenantID,
			Metadata: privatev1.Metadata_builder{
				Name:       tenantName,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
			}.Build(),
		}.Build()
		return f.run(ctx, tenant)
	}

	listTopics := func() []strimzi.KafkaTopic {
		list := &strimzi.KafkaTopicList{}
		Expect(topicsEnv.client.List(ctx, list, &clnt.ListOptions{Namespace: kafkaNamespace})).To(Succeed())
		return list.Items
	}

	getTopic := func(name string) *strimzi.KafkaTopic {
		topic := &strimzi.KafkaTopic{}
		Expect(topicsEnv.client.Get(ctx, clnt.ObjectKey{Namespace: kafkaNamespace, Name: name}, topic)).To(Succeed())
		return topic
	}

	BeforeEach(func() {
		for _, topic := range listTopics() {
			Expect(topicsEnv.client.Delete(ctx, &topic)).To(Succeed())
		}
		Eventually(func() int { return len(listTopics()) }).Should(BeZero())
	})

	It("creates the event, heartbeat and dead-letter topics of the tenant", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		f, hubCache := newTopicsFunction(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), kafkaHubID).
			Return(&controllers.HubEntry{Client: topicsEnv.client}, nil)

		Expect(reconcileTenant(f)).To(Succeed())

		topics := listTopics()
		Expect(topics).To(HaveLen(3))
		for _, topic := range topics {
			Expect(topic.Labels[strimzi.ClusterLabel]).To(Equal(clusterName))
			Expect(topic.Labels[labels.TenantUuid]).To(Equal(tenantName))
			Expect(topic.Labels[labels.TenantID]).To(Equal(tenantID))
			Expect(topic.Spec.Partitions).To(Equal(int32(1)))
			Expect(topic.Spec.Config).To(Equal(map[string]string{
				"retention.ms":    "604800000",
				"retention.bytes": "-1",
				"cleanup.policy":  "delete",
			}))
		}
	})

	It("corrects a topic that exists with the wrong retention", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		existing := &strimzi.KafkaTopic{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: kafkaNamespace,
				Name:      kafkatopics.EventsPrefix + tenantName,
			},
			Spec: strimzi.KafkaTopicSpec{
				Partitions: 1,
				Config:     map[string]string{"retention.ms": "1000"},
			},
		}
		Expect(topicsEnv.client.Create(ctx, existing)).To(Succeed())

		f, hubCache := newTopicsFunction(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), kafkaHubID).
			Return(&controllers.HubEntry{Client: topicsEnv.client}, nil)

		Expect(reconcileTenant(f)).To(Succeed())

		topic := getTopic(kafkatopics.EventsPrefix + tenantName)
		Expect(topic.Spec.Config["retention.ms"]).To(Equal("604800000"))
		Expect(listTopics()).To(HaveLen(3))
	})

	It("leaves topics that already match alone on re-reconciliation", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		f, hubCache := newTopicsFunction(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), kafkaHubID).
			Return(&controllers.HubEntry{Client: topicsEnv.client}, nil).
			AnyTimes()

		Expect(reconcileTenant(f)).To(Succeed())
		versions := map[string]string{}
		for _, topic := range listTopics() {
			versions[topic.Name] = topic.ResourceVersion
		}

		Expect(reconcileTenant(f)).To(Succeed())

		for name, version := range versions {
			Expect(getTopic(name).ResourceVersion).To(Equal(version))
		}
	})

	It("keeps the topics when the tenant is deleted", func() {
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		f, hubCache := newTopicsFunction(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), kafkaHubID).
			Return(&controllers.HubEntry{Client: topicsEnv.client}, nil)
		f.projectsClient.(*MockProjectsClient).EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			AnyTimes()
		f.tenantsClient.(*MockTenantsClient).EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.TenantsUpdateRequest, opts ...grpc.CallOption) (
				*privatev1.TenantsUpdateResponse, error,
			) {
				return &privatev1.TenantsUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		Expect(reconcileTenant(f)).To(Succeed())
		Expect(listTopics()).To(HaveLen(3))

		tenant := privatev1.Tenant_builder{
			Id: tenantID,
			Metadata: privatev1.Metadata_builder{
				Name:              tenantName,
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
		}.Build()
		Expect(f.run(ctx, tenant)).To(Succeed())

		Expect(listTopics()).To(HaveLen(3))
	})
})
