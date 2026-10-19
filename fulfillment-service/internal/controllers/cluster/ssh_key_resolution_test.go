/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package cluster

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const reconcilerTestSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

var _ = Describe("Cluster SSH key resolution", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)
	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	newTask := func(client privatev1.SecretsClient, inline string) *task {
		builder := privatev1.ClusterSpec_builder{}
		if inline != "" {
			builder.SshPublicKey = &inline
		}
		return &task{
			r:       &function{secretsClient: client},
			cluster: privatev1.Cluster_builder{Spec: builder.Build()}.Build(),
		}
	}

	It("resolves public_key into ClusterOrder and takes precedence over inline", func() {
		client := NewMockSecretsClient(ctrl)
		client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, request *privatev1.SecretsGetRequest,
				_ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
				Expect(request.GetId()).To(Equal("key-id"))
				return privatev1.SecretsGetResponse_builder{Object: privatev1.Secret_builder{
					Id:       "key-id",
					Metadata: privatev1.Metadata_builder{Name: "login"}.Build(),
					Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
					Data:     map[string][]byte{publicKeyDataKey: []byte(reconcilerTestSSHPublicKey)},
				}.Build()}.Build(), nil
			})
		t := newTask(client, "invalid inline value that must be ignored")
		t.cluster.GetSpec().SetSshKey(privatev1.SecretLocalReference_builder{Id: "key-id"}.Build())
		spec := &osacv1alpha1.ClusterOrderSpec{}

		Expect(t.addExplicitFields(ctx, spec)).To(Succeed())
		Expect(spec.SSHPublicKey).To(Equal(reconcilerTestSSHPublicKey))
	})

	It("preserves the inline compatibility fallback", func() {
		t := newTask(nil, reconcilerTestSSHPublicKey)
		spec := &osacv1alpha1.ClusterOrderSpec{}

		Expect(t.addExplicitFields(ctx, spec)).To(Succeed())
		Expect(spec.SSHPublicKey).To(Equal(reconcilerTestSSHPublicKey))
	})

	It("preserves the provider default when neither key form is set", func() {
		t := newTask(nil, "")
		spec := &osacv1alpha1.ClusterOrderSpec{}

		Expect(t.addExplicitFields(ctx, spec)).To(Succeed())
		Expect(spec.SSHPublicKey).To(BeEmpty())
	})

	DescribeTable("returns actionable permanent resolution errors",
		func(secret *privatev1.Secret, getErr error, message string) {
			client := NewMockSecretsClient(ctrl)
			response := privatev1.SecretsGetResponse_builder{Object: secret}.Build()
			client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, getErr)
			t := newTask(client, "")
			_, err := t.resolveSSHKey(ctx, privatev1.SecretLocalReference_builder{Id: "key-id"}.Build())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errSSHKeyResolution)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring(message))
		},
		Entry("missing", nil, status.Error(codes.NotFound, "missing"), "not found"),
		Entry("unauthorized", nil, status.Error(codes.PermissionDenied, "denied"), "not authorized"),
		Entry("wrong type", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_VALUE,
			Data: map[string][]byte{"value": []byte("value")},
		}.Build(), nil, "expected SECRET_TYPE_SSH_PUBLIC_KEY"),
		Entry("missing public_key", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"other": []byte("value")},
		}.Build(), nil, "non-empty 'public_key'"),
		Entry("malformed public_key", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{publicKeyDataKey: []byte("not-an-ssh-key")},
		}.Build(), nil, "invalid OpenSSH public key"),
		Entry("deleted", privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{publicKeyDataKey: []byte(reconcilerTestSSHPublicKey)},
		}.Build(), nil, "has been deleted"),
	)

	DescribeTable("fails reconciliation without creating a ClusterOrder",
		func(secret *privatev1.Secret, getErr error, message string) {
			const (
				clusterID    = "ssh-key-failure-cluster"
				tenantName   = "tenant-a"
				hubID        = "hub-a"
				hubNamespace = "hub-a-ns"
			)
			scheme := runtime.NewScheme()
			Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			hubCache := controllers.NewMockHubCache(ctrl)
			hubCache.EXPECT().Get(gomock.Any(), hubID).Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)
			secretsClient := NewMockSecretsClient(ctrl)
			secretsClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(
				privatev1.SecretsGetResponse_builder{Object: secret}.Build(), getErr,
			)

			var updateRequest *privatev1.ClustersUpdateRequest
			clustersClient := NewMockClustersClient(ctrl)
			clustersClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ClustersUpdateRequest,
					_ ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
					updateRequest = request
					return privatev1.ClustersUpdateResponse_builder{Object: request.GetObject()}.Build(), nil
				})

			cluster := privatev1.Cluster_builder{
				Id: clusterID,
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{finalizers.Controller},
					Tenant:     tenantName,
				}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: &privatev1.ClusterTemplateReference{Name: "template-a"},
					SshKey:   privatev1.SecretLocalReference_builder{Id: "key-id"}.Build(),
				}.Build(),
				Status: privatev1.ClusterStatus_builder{
					State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
					Hub:   hubID,
				}.Build(),
			}.Build()
			f := &function{
				logger:         logger,
				hubCache:       hubCache,
				clustersClient: clustersClient,
				secretsClient:  secretsClient,
			}

			Expect(f.run(ctx, cluster)).To(Succeed())
			Expect(updateRequest).ToNot(BeNil())
			var resolutionFailure *privatev1.ClusterCondition
			for _, condition := range updateRequest.GetObject().GetStatus().GetConditions() {
				if condition.GetReason() == "SecretResolutionFailed" {
					resolutionFailure = condition
					break
				}
			}
			Expect(resolutionFailure).ToNot(BeNil())
			Expect(resolutionFailure.GetMessage()).To(ContainSubstring(message))
			orders := &osacv1alpha1.ClusterOrderList{}
			Expect(fakeClient.List(ctx, orders)).To(Succeed())
			Expect(orders.Items).To(BeEmpty())
		},
		Entry("when the Secret is missing", nil, status.Error(codes.NotFound, "missing"), "not found"),
		Entry("when the Secret data is malformed", privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{publicKeyDataKey: []byte("not-an-ssh-key")},
		}.Build(), nil, "invalid OpenSSH public key"),
		Entry("when the Secret was deleted", privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{publicKeyDataKey: []byte(reconcilerTestSSHPublicKey)},
		}.Build(), nil, "has been deleted"),
	)
})
