/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatCluster(cluster *publicv1.Cluster) string {
	var buf bytes.Buffer
	renderCluster(&buf, cluster)
	return buf.String()
}

var _ = Describe("Rendering tests", func() {
	It("should display all fields when set", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-001",
			Spec: &publicv1.ClusterSpec{
				Template: "tpl-ha-001",
			},
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(ContainSubstring("cluster-001"))
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("READY"))
	})

	It("should show '-' for catalog item when spec is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-002",
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
	})

	It("should show '-' for state when status is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-003",
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(MatchRegexp(`State:\s+-`))
	})

	It("should strip CLUSTER_STATE_ prefix from state", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-004",
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(ContainSubstring("READY"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_STATE_"))
	})

	It("should strip CLUSTER_STATE_ prefix from PROGRESSING state", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-005",
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING,
			},
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(ContainSubstring("PROGRESSING"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_STATE_"))
	})

	It("should NOT show Template: row", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-006",
			Spec: &publicv1.ClusterSpec{
				Template: "tpl-ha-001",
			},
		}.Build()
		output := formatCluster(cluster)
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
	})

	It("should show '-' for catalog item when spec is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-007",
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
	})

	It("should show catalog item ID when set", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-008",
			Spec: publicv1.ClusterSpec_builder{
				CatalogItem: "my-catalog-item-id",
			}.Build(),
		}.Build()
		output := formatCluster(cluster)
		Expect(output).To(MatchRegexp(`Catalog Item:\s+my-catalog-item-id`))
	})
})
