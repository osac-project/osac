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
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func formatCluster(cluster *publicv1.Cluster, version *publicv1.ClusterVersion) (string, error) {
	var buf bytes.Buffer
	err := renderCluster(&buf, cluster, version)
	return buf.String(), err
}

var _ = Describe("Rendering tests", func() {
	It("should display all fields when set", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-001",
			Spec: &publicv1.ClusterSpec{
				Template: publicv1.ClusterTemplateReference_builder{Id: "tpl-ha-001"}.Build(),
			},
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("cluster-001"))
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("READY"))
	})

	It("should show '-' for catalog item when spec is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-002",
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
	})

	It("should show '-' for state when status is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-003",
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`State:\s+-`))
	})

	It("should strip CLUSTER_STATE_ prefix from state", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-004",
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
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
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("PROGRESSING"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_STATE_"))
	})

	It("should NOT show Template: row", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-006",
			Spec: &publicv1.ClusterSpec{
				Template: publicv1.ClusterTemplateReference_builder{Id: "tpl-ha-001"}.Build(),
			},
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(ContainSubstring("Template:"))
		Expect(output).To(ContainSubstring("Catalog Item:"))
	})

	It("should show '-' for catalog item when spec is nil", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-007",
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`Catalog Item:\s+-`))
	})

	It("should show catalog item ID when set", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-008",
			Spec: publicv1.ClusterSpec_builder{
				CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: "my-catalog-item-id"}.Build(),
			}.Build(),
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`Catalog Item:\s+my-catalog-item-id`))
	})
})

var _ = Describe("Version rendering tests", func() {
	It("should show version details when version is present and ACTIVE", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-v1",
			Spec: publicv1.ClusterSpec_builder{
				Version: &publicv1.ClusterVersionReference{Name: "4-17-0"},
			}.Build(),
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		version := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output, err := formatCluster(cluster, version)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("\nDesired Version:\n"))
		Expect(output).To(MatchRegexp(`Name:\s+4-17-0`))
		Expect(output).To(MatchRegexp(`(?m)^ +Version: +4\.17\.0$`))
		Expect(output).To(MatchRegexp(`State:\s+ACTIVE`))
		Expect(output).NotTo(ContainSubstring("Deprecated At:"))
		Expect(output).NotTo(ContainSubstring("Obsolete At:"))
	})

	It("should show deprecation timestamps when version is DEPRECATED", func() {
		depTime := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
		obsTime := time.Date(2026, 10, 15, 10, 30, 0, 0, time.UTC)
		cluster := publicv1.Cluster_builder{
			Id: "cluster-v2",
			Spec: publicv1.ClusterSpec_builder{
				Version: &publicv1.ClusterVersionReference{Name: "4-16-0"},
			}.Build(),
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		version := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.16.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED,
				Deprecation: publicv1.ClusterVersionDeprecation_builder{
					DeprecationTimestamp:  timestamppb.New(depTime),
					ObsolescenceTimestamp: timestamppb.New(obsTime),
				}.Build(),
			}.Build(),
		}.Build()
		output, err := formatCluster(cluster, version)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("\nDesired Version:\n"))
		Expect(output).To(MatchRegexp(`Name:\s+4-16-0`))
		Expect(output).To(MatchRegexp(`(?m)^ +Version: +4\.16\.0$`))
		Expect(output).To(MatchRegexp(`State:\s+DEPRECATED`))
		Expect(output).To(MatchRegexp(`Deprecated At:\s+2026-07-15T10:30:00Z`))
		Expect(output).To(MatchRegexp(`Obsolete At:\s+2026-10-15T10:30:00Z`))
	})

	It("should omit version section when version is not set", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-v3",
			Status: &publicv1.ClusterStatus{
				State: publicv1.ClusterState_CLUSTER_STATE_READY,
			},
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(ContainSubstring("\nDesired Version:\n"))
	})

	It("should show version name but '-' for resolved fields when version lookup failed", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-v4",
			Spec: publicv1.ClusterSpec_builder{
				Version: &publicv1.ClusterVersionReference{Name: "4-17-0"},
			}.Build(),
		}.Build()
		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("\nDesired Version:\n"))
		Expect(output).To(MatchRegexp(`Name:\s+4-17-0`))
		Expect(output).To(MatchRegexp(`(?m)^ +Version: +-$`))
		Expect(output).To(MatchRegexp(`State:\s+-`))
	})

	It("should strip CLUSTER_VERSION_STATE_ prefix from OBSOLETE state", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-v5",
			Spec: publicv1.ClusterSpec_builder{
				Version: &publicv1.ClusterVersionReference{Name: "4-15-0"},
			}.Build(),
		}.Build()
		version := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.15.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
			}.Build(),
		}.Build()
		output, err := formatCluster(cluster, version)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("OBSOLETE"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_VERSION_STATE_"))
	})
})

var _ = Describe("Upgrade rendering tests", func() {
	It("renders current versions including those matching the desired version", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-upgrade-001",
			Spec: publicv1.ClusterSpec_builder{
				Version: &publicv1.ClusterVersionReference{Name: "4-17-0"},
			}.Build(),
			Status: publicv1.ClusterStatus_builder{
				State:           publicv1.ClusterState_CLUSTER_STATE_PROGRESSING,
				ObservedVersion: "4.17.0",
				NodeSets: map[string]*publicv1.ClusterNodeSetStatus{
					"gpu": publicv1.ClusterNodeSetStatus_builder{
						Size:            proto.Int32(2),
						ObservedVersion: "4.17.0",
					}.Build(),
					"compute": publicv1.ClusterNodeSetStatus_builder{
						Size:            proto.Int32(4),
						ObservedVersion: "4.18.0",
					}.Build(),
				},
			}.Build(),
		}.Build()

		version := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{Version: "4.17.0"}.Build(),
		}.Build()
		output, err := formatCluster(cluster, version)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`(?m)^Control Plane Current Version: +4\.17\.0$`))
		Expect(output).To(ContainSubstring("Node Sets:"))
		Expect(output).To(MatchRegexp(`(?m)^ +Name +Size +Current Version$`))
		Expect(output).To(MatchRegexp(`compute\s+4\s+4\.18\.0`))
		Expect(output).To(MatchRegexp(`gpu\s+2\s+4\.17\.0`))
		Expect(strings.Index(output, "compute")).To(BeNumerically("<", strings.Index(output, "gpu")))
	})

	It("renders whole-cluster history newest first with all fields", func() {
		accepted := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
		transition := time.Date(2026, 11, 1, 9, 5, 0, 0, time.UTC)
		olderAccepted := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		olderTransition := time.Date(2026, 10, 1, 9, 5, 0, 0, time.UTC)
		cluster := publicv1.Cluster_builder{
			Id: "cluster-upgrade-002",
			Status: publicv1.ClusterStatus_builder{
				UpgradeHistory: []*publicv1.ClusterUpgradeHistoryEntry{
					publicv1.ClusterUpgradeHistoryEntry_builder{
						SourceVersion:       "4.17.0",
						TargetVersion:       "4.18.0",
						AcceptedTime:        timestamppb.New(accepted),
						StateTransitionTime: timestamppb.New(transition),
						State:               publicv1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING,
						Message:             "Node set \"gpu\" is upgrading",
					}.Build(),
					publicv1.ClusterUpgradeHistoryEntry_builder{
						SourceVersion:       "4.16.0",
						TargetVersion:       "4.17.0",
						AcceptedTime:        timestamppb.New(olderAccepted),
						StateTransitionTime: timestamppb.New(olderTransition),
						State:               publicv1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
						Message:             "",
					}.Build(),
				},
			}.Build(),
		}.Build()

		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("Upgrade History:"))
		Expect(output).To(MatchRegexp(`4\.17\.0\s+4\.18\.0\s+PROGRESSING\s+2026-11-01T09:00:00Z\s+2026-11-01T09:05:00Z\s+Node set "gpu" is upgrading`))
		Expect(output).To(MatchRegexp(`4\.16\.0\s+4\.17\.0\s+SUCCEEDED\s+2026-10-01T09:00:00Z\s+2026-10-01T09:05:00Z\s+-`))
		Expect(strings.Index(output, "4.18.0")).To(BeNumerically("<", strings.Index(output, "4.16.0")))
		Expect(output).NotTo(ContainSubstring("Component:"))
	})

	It("renders missing upgrade fields as dashes", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-upgrade-003",
			Status: publicv1.ClusterStatus_builder{
				NodeSets: map[string]*publicv1.ClusterNodeSetStatus{
					"compute": publicv1.ClusterNodeSetStatus_builder{}.Build(),
				},
				UpgradeHistory: []*publicv1.ClusterUpgradeHistoryEntry{
					publicv1.ClusterUpgradeHistoryEntry_builder{}.Build(),
				},
			}.Build(),
		}.Build()

		output, err := formatCluster(cluster, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(MatchRegexp(`(?m)^Control Plane Current Version: +-$`))
		Expect(output).To(MatchRegexp(`compute\s+-\s+-`))
		Expect(output).To(MatchRegexp(`-\s+-\s+UNSPECIFIED\s+-\s+-\s+-`))
	})

	It("propagates node set rendering errors", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-upgrade-error-001",
			Status: publicv1.ClusterStatus_builder{
				NodeSets: map[string]*publicv1.ClusterNodeSetStatus{
					"compute": publicv1.ClusterNodeSetStatus_builder{}.Build(),
				},
			}.Build(),
		}.Build()

		err := renderCluster(errorWriter{}, cluster, nil)
		Expect(err).To(MatchError("write failed"))
	})

	It("propagates summary rendering errors", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-summary-error",
		}.Build()

		err := renderCluster(errorWriter{}, cluster, nil)
		Expect(err).To(MatchError("write failed"))
	})

	It("propagates upgrade history rendering errors", func() {
		cluster := publicv1.Cluster_builder{
			Id: "cluster-upgrade-error-002",
			Status: publicv1.ClusterStatus_builder{
				UpgradeHistory: []*publicv1.ClusterUpgradeHistoryEntry{
					publicv1.ClusterUpgradeHistoryEntry_builder{}.Build(),
				},
			}.Build(),
		}.Build()

		err := renderCluster(errorWriter{}, cluster, nil)
		Expect(err).To(MatchError("write failed"))
	})
})
