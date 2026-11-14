/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package workloadcluster

import (
	"bytes"
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/metautil"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("WorkloadCluster command", func() {
	It("registers the four required subcommands", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("workload-cluster"))
		Expect(cmd.Commands()).To(HaveLen(4))
		names := make([]string, 0, len(cmd.Commands()))
		for _, subcommand := range cmd.Commands() {
			names = append(names, subcommand.Name())
		}
		Expect(names).To(ConsistOf("create", "list", "describe", "update"))
	})

	It("registers create flags", func() {
		cmd := createCmd()
		for _, name := range []string{"name", "source-hub", "managed-cluster-name", "supported-workloads"} {
			Expect(cmd.Flags().Lookup(name)).NotTo(BeNil(), name)
		}
	})

	It("requires an update flag", func() {
		args := &updateArgs{}
		cmd := updateCmd()
		err := runUpdate(cmd, "cluster-1", args)
		Expect(err).To(MatchError("at least one update flag must be specified"))
	})
})

var _ = Describe("WorkloadCluster CLI input", func() {
	It("accepts comma-separated MaaS capabilities and removes duplicates", func() {
		workloads, err := parseSupportedWorkloads([]string{"maas,maas"})
		Expect(err).NotTo(HaveOccurred())
		Expect(workloads).To(Equal([]string{"maas"}))
	})

	It("rejects unsupported capabilities", func() {
		_, err := parseSupportedWorkloads([]string{"vmaas"})
		Expect(err).To(MatchError("unsupported workload \"vmaas\"; only 'maas' is supported"))
	})

	It("parses labels", func() {
		labels, err := metautil.ParseLabels([]string{"environment=test", "owner=ai-grid"})
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(Equal(map[string]string{"environment": "test", "owner": "ai-grid"}))
	})

	It("preserves commas and equals signs in repeated label values", func() {
		labels, err := metautil.ParseLabels([]string{"description=region=us-east, gpu", "owner=ai-grid"})
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(Equal(map[string]string{"description": "region=us-east, gpu", "owner": "ai-grid"}))
	})

	It("rejects malformed labels", func() {
		_, err := metautil.ParseLabels([]string{"environment"})
		Expect(err).To(MatchError("invalid label \"environment\": must be in key=value format"))
	})
})

var _ = Describe("WorkloadCluster rendering", func() {
	It("renders registration details and condition reasons", func() {
		object := privatev1.WorkloadCluster_builder{
			Id:       "wc-1",
			Metadata: privatev1.Metadata_builder{Name: "worker-1"}.Build(),
			Spec: privatev1.WorkloadClusterSpec_builder{
				SourceHubId:        "hub-1",
				ManagedClusterName: "worker-1",
				SupportedWorkloads: []string{"maas"},
			}.Build(),
			Status: privatev1.WorkloadClusterStatus_builder{
				Conditions: []*privatev1.WorkloadClusterCondition{
					privatev1.WorkloadClusterCondition_builder{
						Type: "Available", Status: "False", Reason: "ManagedClusterUnavailable",
					}.Build(),
				},
			}.Build(),
		}.Build()

		output := &bytes.Buffer{}
		Expect(renderWorkloadCluster(output, object)).To(Succeed())
		Expect(output.String()).To(MatchRegexp(`Source Hub ID:\s+hub-1`))
		Expect(output.String()).To(MatchRegexp(`ManagedCluster:\s+worker-1`))
		Expect(output.String()).To(ContainSubstring("ManagedClusterUnavailable"))
	})

	It("renders a compact list with availability state", func() {
		object := privatev1.WorkloadCluster_builder{
			Id:       "wc-1",
			Metadata: privatev1.Metadata_builder{Name: "worker-1"}.Build(),
			Spec: privatev1.WorkloadClusterSpec_builder{
				SourceHubId:        "hub-1",
				ManagedClusterName: "worker-1",
			}.Build(),
			Status: privatev1.WorkloadClusterStatus_builder{
				Conditions: []*privatev1.WorkloadClusterCondition{
					privatev1.WorkloadClusterCondition_builder{Type: "Available", Status: "True"}.Build(),
					privatev1.WorkloadClusterCondition_builder{Type: "HubReachable", Status: "True"}.Build(),
				},
			}.Build(),
		}.Build()
		output := &bytes.Buffer{}
		renderWorkloadClusterList(output, []*privatev1.WorkloadCluster{object}, map[string]string{"hub-1": "ai-grid-hub"})
		Expect(output.String()).To(MatchRegexp(`wc-1\s+worker-1\s+ai-grid-hub\s+worker-1\s+True\s+True`))
	})

	It("falls back to the Hub ID when the Hub is not returned", func() {
		object := privatev1.WorkloadCluster_builder{
			Id:       "wc-1",
			Metadata: privatev1.Metadata_builder{Name: "worker-1"}.Build(),
			Spec:     privatev1.WorkloadClusterSpec_builder{SourceHubId: "hub-1"}.Build(),
		}.Build()
		output := &bytes.Buffer{}
		renderWorkloadClusterList(output, []*privatev1.WorkloadCluster{object}, nil)
		Expect(output.String()).To(MatchRegexp(`wc-1\s+worker-1\s+hub-1`))
	})
})

var _ = Describe("WorkloadCluster client request errors", func() {
	It("rejects an empty reference before making a request", func() {
		_, err := findWorkloadCluster(context.Background(), nil, "")
		Expect(err).To(MatchError("workload cluster name or ID must not be empty"))
	})
})
