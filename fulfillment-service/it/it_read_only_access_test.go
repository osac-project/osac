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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type readerTestAssets struct {
	instanceTypeID string
	templateID     string
}

var _ = Describe("Reader API access", func() {
	var (
		assets readerTestAssets
	)

	BeforeEach(func(ctx context.Context) {
		_, _ = getOrCreateTenantDefaultNetworkClassFixture(ctx)
		assets = readerTestAssets{
			instanceTypeID: createCatalogItemBareMetalInstanceTypeFixture(ctx, ""),
			templateID:     createCatalogItemClusterTemplateFixture(ctx, nil, nil),
		}
	})

	It("keeps tenant-reader reads in scope and denies cluster mutations", func(ctx context.Context) {
		oldConn, oldTokenSource := newReaderTestConnection(ctx, userUsername)
		Expect(readerTokenRoles(ctx, oldTokenSource)).ToNot(ContainElement("tenant-reader"))
		readerCluster := createReaderTestCluster(
			ctx, oldConn, usersGroup, "reader-in-scope-"+uuid.New()[24:], assets,
		)

		otherConn, _ := newReaderTestConnection(ctx, "ben")
		otherCluster := createReaderTestCluster(
			ctx, otherConn, "development", "reader-out-of-scope-"+uuid.New()[24:], assets,
		)

		readerConn, _ := createReaderRoleBindingAndConnection(ctx, userUsername, "tenant-reader")
		readerClient := publicv1.NewClustersClient(readerConn)

		getResponse, err := readerClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: readerCluster.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(readerCluster.GetId()))

		filter := fmt.Sprintf("this.metadata.name in [%q, %q]",
			readerCluster.GetMetadata().GetName(), otherCluster.GetMetadata().GetName())
		listResponse, err := readerClient.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterNames(listResponse.GetItems())).To(ContainElement(readerCluster.GetMetadata().GetName()))
		Expect(clusterNames(listResponse.GetItems())).ToNot(ContainElement(otherCluster.GetMetadata().GetName()))

		_, err = readerClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: otherCluster.GetId(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))

		createdName := "reader-create-denied-" + uuid.New()[24:]
		createResponse, err := readerClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: readerTestClusterObject(createdName, usersGroup, assets),
		}.Build())
		if err == nil {
			Expect(createResponse).ToNot(BeNil())
			Expect(createResponse.GetObject()).ToNot(BeNil())
			deferClusterDeletion(createResponse.GetObject().GetId(), tool.InternalView().AdminConn())
		}
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		updatedCluster := proto.Clone(readerCluster).(*publicv1.Cluster)
		updatedCluster.GetSpec().GetNodeSets()["my-node-set"].SetSize(4)
		_, err = readerClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: updatedCluster,
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		_, err = readerClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
			Id: readerCluster.GetId(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		adminClient := publicv1.NewClustersClient(tool.InternalView().AdminConn())
		persistedResponse, err := adminClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: readerCluster.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		persistedCluster := persistedResponse.GetObject()
		Expect(persistedCluster.GetSpec().GetNodeSets()["my-node-set"].GetSize()).To(Equal(int32(3)))
		Expect(persistedCluster.GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		createFilter := fmt.Sprintf("this.metadata.name == %q", createdName)
		persistedList, err := adminClient.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: &createFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persistedList.GetItems()).To(BeEmpty())
	})

	It("keeps cloud-provider-reader in the user's existing scope", func(ctx context.Context) {
		oldConn, _ := newReaderTestConnection(ctx, "charles")
		inScopeCluster := createReaderTestCluster(
			ctx, oldConn, "sales", "cloud-reader-in-scope-"+uuid.New()[24:], assets,
		)

		otherConn, _ := newReaderTestConnection(ctx, "ben")
		outOfScopeCluster := createReaderTestCluster(
			ctx, otherConn, "development", "cloud-reader-out-of-scope-"+uuid.New()[24:], assets,
		)

		readerConn, _ := createReaderRoleBindingAndConnection(ctx, "charles", "cloud-provider-reader")
		readerClient := publicv1.NewClustersClient(readerConn)

		getResponse, err := readerClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: inScopeCluster.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(inScopeCluster.GetId()))

		filter := fmt.Sprintf("this.metadata.name in [%q, %q]",
			inScopeCluster.GetMetadata().GetName(), outOfScopeCluster.GetMetadata().GetName())
		listResponse, err := readerClient.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterNames(listResponse.GetItems())).To(ContainElement(inScopeCluster.GetMetadata().GetName()))
		Expect(clusterNames(listResponse.GetItems())).ToNot(ContainElement(outOfScopeCluster.GetMetadata().GetName()))

		_, err = readerClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: outOfScopeCluster.GetId(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})
})

func readerTestClusterObject(name, tenant string, assets readerTestAssets) *publicv1.Cluster {
	return publicv1.Cluster_builder{
		Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		Spec: publicv1.ClusterSpec_builder{
			Template: publicv1.ClusterTemplateReference_builder{Id: assets.templateID}.Build(),
			NodeSets: testClusterNodeSets(assets.instanceTypeID, 3),
		}.Build(),
	}.Build()
}

func createReaderTestAssets(ctx context.Context) readerTestAssets {
	GinkgoHelper()
	_, _ = getOrCreateTenantDefaultNetworkClassFixture(ctx)
	return readerTestAssets{
		instanceTypeID: createCatalogItemBareMetalInstanceTypeFixture(ctx, ""),
		templateID:     createCatalogItemClusterTemplateFixture(ctx, nil, nil),
	}
}

func readerTestClusterCreateRequest(name, tenant string, assets readerTestAssets) *publicv1.ClustersCreateRequest {
	return publicv1.ClustersCreateRequest_builder{
		Object: readerTestClusterObject(name, tenant, assets),
	}.Build()
}

func createReaderTestCluster(
	ctx context.Context,
	conn *grpc.ClientConn,
	tenant, name string,
	assets readerTestAssets,
) *publicv1.Cluster {
	GinkgoHelper()
	client := publicv1.NewClustersClient(conn)
	response, err := client.Create(ctx, readerTestClusterCreateRequest(name, tenant, assets))
	Expect(err).ToNot(HaveOccurred())
	cluster := response.GetObject()
	deferClusterDeletion(cluster.GetId(), tool.InternalView().AdminConn())
	return cluster
}

func newReaderTestConnection(ctx context.Context, username string) (*grpc.ClientConn, auth.TokenSource) {
	GinkgoHelper()
	tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
	Expect(err).ToNot(HaveOccurred())
	_, err = tokenSource.Token(ctx)
	Expect(err).ToNot(HaveOccurred())
	conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(conn.Close()).To(Succeed())
	})
	return conn, tokenSource
}

func createReaderRoleBindingAndConnection(
	ctx context.Context,
	username, roleName string,
) (*grpc.ClientConn, auth.TokenSource) {
	GinkgoHelper()
	identityConn, _ := newReaderTestConnection(ctx, username)
	_, err := publicv1.NewClustersClient(identityConn).List(ctx, publicv1.ClustersListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())

	usersClient := privatev1.NewUsersClient(tool.InternalView().AdminConn())
	filter := fmt.Sprintf("this.spec.username == %q", username)
	usersResponse, err := usersClient.List(ctx, privatev1.UsersListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(1)),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	Expect(usersResponse.GetItems()).To(HaveLen(1))

	createReaderRoleBinding(ctx, usersResponse.GetItems()[0].GetId(), roleName)
	conn, tokenSource := newReaderTestConnection(ctx, username)
	Expect(readerTokenRoles(ctx, tokenSource)).To(ContainElement(roleName))
	return conn, tokenSource
}

func createReaderRoleBinding(ctx context.Context, userID, roleName string) string {
	GinkgoHelper()
	client := privatev1.NewRoleBindingsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
		Object: privatev1.RoleBinding_builder{
			Metadata: privatev1.Metadata_builder{Name: "reader-" + uuid.New()}.Build(),
			Spec: privatev1.RoleBindingSpec_builder{
				Role: privatev1.RoleReference_builder{Name: roleName}.Build(),
				Users: []*privatev1.UserReference{
					privatev1.UserReference_builder{Id: userID}.Build(),
				},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	id := response.GetObject().GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		_, err := client.Delete(cleanupCtx, privatev1.RoleBindingsDeleteRequest_builder{Id: id}.Build())
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return
		}
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			_, err := client.Get(cleanupCtx, privatev1.RoleBindingsGetRequest_builder{Id: id}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}, time.Minute, time.Second).Should(Succeed())
	})

	Eventually(func(g Gomega) {
		binding, err := client.Get(ctx, privatev1.RoleBindingsGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		status := binding.GetObject().GetStatus()
		g.Expect(status.GetState()).To(
			Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY),
			"role binding status message: %s", status.GetMessage(),
		)
	}, time.Minute, time.Second).Should(Succeed())
	return id
}

func readerTokenRoles(ctx context.Context, tokenSource auth.TokenSource) []string {
	GinkgoHelper()
	token, err := tokenSource.Token(ctx)
	Expect(err).ToNot(HaveOccurred())
	claims, err := parseJWTClaims(token.Access)
	Expect(err).ToNot(HaveOccurred())
	realmAccess, ok := claims["realm_access"].(map[string]any)
	Expect(ok).To(BeTrue())
	roleClaims, ok := realmAccess["roles"].([]any)
	Expect(ok).To(BeTrue())
	roles := make([]string, 0, len(roleClaims))
	for _, role := range roleClaims {
		if name, ok := role.(string); ok {
			roles = append(roles, name)
		}
	}
	return roles
}

func clusterNames(clusters []*publicv1.Cluster) []string {
	names := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		names = append(names, cluster.GetMetadata().GetName())
	}
	return names
}
