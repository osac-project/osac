/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversion

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

func formatClusterVersion(cv *publicv1.ClusterVersion) string {
	var buf bytes.Buffer
	renderClusterVersion(&buf, cv)
	return buf.String()
}

var _ = Describe("Rendering tests", func() {
	It("should display all base fields for ACTIVE version", func() {
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-001",
			Metadata: publicv1.Metadata_builder{
				Name: "4-17-0",
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version:   "4.17.0",
				State:     publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				Enabled:   proto.Bool(true),
				IsDefault: proto.Bool(false),
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).To(MatchRegexp(`Name:\s+4-17-0`))
		Expect(output).To(MatchRegexp(`Version:\s+4\.17\.0`))
		Expect(output).To(ContainSubstring("ACTIVE"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_VERSION_STATE_"))
		Expect(output).To(MatchRegexp(`Enabled:\s+true`))
		Expect(output).To(MatchRegexp(`Default:\s+false`))
	})

	It("should strip CLUSTER_VERSION_STATE_ prefix from DEPRECATED state", func() {
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-002",
			Metadata: publicv1.Metadata_builder{
				Name: "4-16-0",
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.16.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED,
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).To(ContainSubstring("DEPRECATED"))
		Expect(output).NotTo(ContainSubstring("CLUSTER_VERSION_STATE_"))
	})

	It("should show deprecation timestamps when present", func() {
		depTime := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
		obsTime := time.Date(2026, 10, 15, 10, 30, 0, 0, time.UTC)
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-003",
			Metadata: publicv1.Metadata_builder{
				Name: "4-16-0",
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.16.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED,
				Deprecation: publicv1.ClusterVersionDeprecation_builder{
					DeprecationTimestamp:  timestamppb.New(depTime),
					ObsolescenceTimestamp: timestamppb.New(obsTime),
				}.Build(),
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).To(MatchRegexp(`Deprecated At:\s+2026-07-15T10:30:00Z`))
		Expect(output).To(MatchRegexp(`Obsolete At:\s+2026-10-15T10:30:00Z`))
	})

	It("should omit deprecation section when absent", func() {
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-004",
			Metadata: publicv1.Metadata_builder{
				Name: "4-17-0",
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
				State:   publicv1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).NotTo(ContainSubstring("Deprecated At:"))
		Expect(output).NotTo(ContainSubstring("Obsolete At:"))
	})

	It("should show '-' for enabled when not set", func() {
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-005",
			Metadata: publicv1.Metadata_builder{
				Name: "4-17-0",
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).To(MatchRegexp(`Enabled:\s+-`))
		Expect(output).To(MatchRegexp(`Default:\s+-`))
	})

	It("should show '-' for name when metadata is nil", func() {
		cv := publicv1.ClusterVersion_builder{
			Id: "cv-009",
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
			}.Build(),
		}.Build()
		output := formatClusterVersion(cv)
		Expect(output).To(MatchRegexp(`Name:\s+-`))
	})
})
