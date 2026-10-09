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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Catalog Item CLI", Label("catalog-items", "cli"), func() {
	DescribeTable("creates a VM with the CLI from a catalog item or a Template", func(ctx context.Context, createFromCatalogItem bool) {
		home, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
		_, stderr, code := tool.LoginCLI(ctx, home, userUsername, usersPassword)
		Expect(code).To(Equal(0), stderr)

		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
			Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			Published: true,
		}.Build())

		sourceFlag, sourceID := "--template", template
		if createFromCatalogItem {
			sourceFlag, sourceID = "--catalog-item", item.GetId()
		}
		name := catalogItemFixtureName()
		_, stderr, code = tool.RunCLI(ctx, home, "create", "computeinstance", sourceFlag, sourceID,
			"--name", name, "--network-attachment",
			"subnet="+network.subnetID+",security-groups="+network.securityGroupID)
		Expect(code).To(Equal(0), stderr)
		client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		listed, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetItems()).To(HaveLen(1))
		created := listed.GetItems()[0]
		deferComputeInstanceDeletion(created.GetId(), tool.ExternalView().UserConn())
		Expect(created.GetSpec().GetTemplate().GetId()).To(Equal(template))
		if createFromCatalogItem {
			Expect(created.GetSpec().GetCatalogItem().GetId()).To(Equal(item.GetId()))
			Eventually(func(g Gomega) {
				objects := &osacv1alpha1.ComputeInstanceList{}
				g.Expect(tool.KubeClient().List(ctx, objects, crclient.MatchingLabels{labels.ComputeInstanceUuid: created.GetId()})).To(Succeed())
				g.Expect(objects.Items).To(HaveLen(1))
				g.Expect(objects.Items[0].Spec.TemplateID).To(Equal(template))
			}, time.Minute, time.Second).Should(Succeed())
		} else {
			Expect(created.GetSpec().HasCatalogItem()).To(BeFalse())
		}
	},
		Entry("from a published catalog item", true),
		Entry("directly from a Template", false),
	)

	It("creates a cluster with the CLI from a catalog item and writes a ClusterOrder", func(ctx context.Context) {
		home, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
		_, stderr, code := tool.LoginCLI(ctx, home, userUsername, usersPassword)
		Expect(code).To(Equal(0), stderr)

		bmit := createCatalogItemBareMetalInstanceTypeFixture(ctx, "shared")
		version := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
		template := createCatalogItemClusterTemplateFixture(ctx, privatev1.ClusterTemplateSpecDefaults_builder{
			Version: privatev1.ClusterVersionReference_builder{Id: version}.Build(),
		}.Build(), nil)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
			Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
			Published: true,
			Fields: publicv1.ClusterCatalogItemFields_builder{
				NetworkAttachment: publicv1.ClusterNetworkAttachmentFieldPolicy_builder{Locked: network.clusterAttachment()}.Build(),
				NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{
					Items: map[string]*publicv1.ClusterCatalogNodeSet{
						"workers": publicv1.ClusterCatalogNodeSet_builder{Size: 2,
							BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmit}.Build(),
						}.Build(),
					},
				}.Build()}.Build(),
			}.Build(),
		}.Build())
		name := catalogItemFixtureName()
		_, stderr, code = tool.RunCLI(ctx, home, "create", "cluster", "--catalog-item", item.GetId(), "--name", name)
		Expect(code).To(Equal(0), stderr)

		client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
		listed, err := client.List(ctx, publicv1.ClustersListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetItems()).To(HaveLen(1))
		created := listed.GetItems()[0]
		deferClusterDeletion(created.GetId(), tool.ExternalView().UserConn())
		Expect(created.GetSpec().GetCatalogItem().GetId()).To(Equal(item.GetId()))
		Expect(created.GetSpec().GetTemplate().GetId()).To(Equal(template))
		Eventually(func(g Gomega) {
			orders := &osacv1alpha1.ClusterOrderList{}
			g.Expect(tool.KubeClient().List(ctx, orders, crclient.MatchingLabels{labels.ClusterOrderUuid: created.GetId()})).To(Succeed())
			g.Expect(orders.Items).To(HaveLen(1))
			g.Expect(orders.Items[0].Spec.TemplateID).To(Equal(template))
		}, time.Minute, time.Second).Should(Succeed())
	})
})
