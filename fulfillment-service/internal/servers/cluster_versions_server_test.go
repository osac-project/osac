/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Public cluster versions server", func() {
	Describe("Behaviour", func() {
		var (
			publicServer         *ClusterVersionsServer
			privateServer        *PrivateClusterVersionsServer
			createClusterVersion func(name, version string, opts ...func(*privatev1.ClusterVersionSpec_builder)) *privatev1.ClusterVersion
		)

		BeforeEach(func() {
			var err error

			// Create the public server:
			publicServer, err = NewClusterVersionsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a private server for test data setup:
			privateServer, err = NewPrivateClusterVersionsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Helper to create a cluster version via the private server.
			createClusterVersion = func(name string, version string,
				opts ...func(*privatev1.ClusterVersionSpec_builder)) *privatev1.ClusterVersion {
				spec := privatev1.ClusterVersionSpec_builder{
					Version: version,
					Image:   "quay.io/ocp:" + version,
				}
				for _, opt := range opts {
					opt(&spec)
				}
				response, err := privateServer.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
					Object: privatev1.ClusterVersion_builder{
						Metadata: privatev1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: spec.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}
		})

		It("Lists ACTIVE and DEPRECATED by default, hides OBSOLETE and disabled", func() {
			active := createClusterVersion("pub-active", "4.17.0")
			deprecated := createClusterVersion("pub-deprecated", "4.16.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.State = privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_DEPRECATED
			})
			obsolete := createClusterVersion("pub-obsolete", "4.15.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.State = privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE
			})
			disabled := createClusterVersion("pub-disabled", "4.14.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.Enabled = new(false)
			})

			// Public List without filter should return only ACTIVE + DEPRECATED + enabled:
			response, err := publicServer.List(ctx,
				publicv1.ClusterVersionsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))

			ids := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				ids[i] = item.GetId()
			}
			Expect(ids).To(ContainElement(active.GetId()))
			Expect(ids).To(ContainElement(deprecated.GetId()))
			Expect(ids).ToNot(ContainElement(obsolete.GetId()))
			Expect(ids).ToNot(ContainElement(disabled.GetId()))
		})

		It("Lists OBSOLETE when filter includes state", func() {
			createClusterVersion("filter-active", "4.17.0")
			obsolete := createClusterVersion("filter-obsolete", "4.15.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.State = privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE
			})

			response, err := publicServer.List(ctx, publicv1.ClusterVersionsListRequest_builder{
				Filter: new(fmt.Sprintf("this.spec.state == %d",
					int32(privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE))),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(obsolete.GetId()))
		})

		It("Lists disabled when filter includes enabled", func() {
			createClusterVersion("enabled-active", "4.17.0")
			disabled := createClusterVersion("enabled-disabled", "4.16.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.Enabled = new(false)
			})

			response, err := publicServer.List(ctx, publicv1.ClusterVersionsListRequest_builder{
				Filter: new("this.spec.enabled == false"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(disabled.GetId()))
		})

		It("Filter by is_default still applies state and enabled defaults", func() {
			// Create an ACTIVE default version and a non-default ACTIVE version:
			defaultCv := createClusterVersion("isdef-active", "4.17.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.IsDefault = new(true)
			})

			createClusterVersion("isdef-other", "4.16.0")

			// Filter by is_default — should still apply state and enabled defaults,
			// returning only the ACTIVE default version:
			response, err := publicServer.List(ctx, publicv1.ClusterVersionsListRequest_builder{
				Filter: new("this.spec.is_default == true"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(defaultCv.GetId()))
		})

		It("Filter by state only still applies enabled default", func() {
			createClusterVersion("state-only-active", "4.17.0")
			createClusterVersion("state-only-obsolete", "4.15.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.State = privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE
				s.Enabled = new(false)
			})

			response, err := publicServer.List(ctx, publicv1.ClusterVersionsListRequest_builder{
				Filter: new(fmt.Sprintf("this.spec.state == %d",
					int32(privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE))),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The disabled OBSOLETE version should be hidden by the enabled default:
			Expect(response.GetItems()).To(BeEmpty())
		})

		It("Gets any cluster version regardless of state", func() {
			obsolete := createClusterVersion("get-obsolete", "4.15.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.State = privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE
			})

			getResponse, err := publicServer.Get(ctx, publicv1.ClusterVersionsGetRequest_builder{
				Id: obsolete.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject()).ToNot(BeNil())
			Expect(getResponse.GetObject().GetId()).To(Equal(obsolete.GetId()))
		})

		It("Public response includes is_default", func() {
			cv := createClusterVersion("default-visible", "4.17.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.IsDefault = new(true)
			})

			getResponse, err := publicServer.Get(ctx, publicv1.ClusterVersionsGetRequest_builder{
				Id: cv.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetIsDefault()).To(BeTrue())
		})

		It("Public Update ignores caller-set is_default", func() {
			// Create a default version via private:
			defaultCv := createClusterVersion("existing-default", "4.17.0", func(s *privatev1.ClusterVersionSpec_builder) {
				s.IsDefault = new(true)
			})

			// Create a non-default version via private:
			other := createClusterVersion("non-default", "4.18.0")

			// Try to set is_default via public Update — should be ignored by the inMapper:
			_, err := publicServer.Update(ctx, publicv1.ClusterVersionsUpdateRequest_builder{
				Object: publicv1.ClusterVersion_builder{
					Id: other.GetId(),
					Spec: publicv1.ClusterVersionSpec_builder{
						IsDefault: new(true),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.is_default"}},
			}.Build())
			// The inMapper ignores is_default, so the update goes through but the field
			// is not changed. Verify the original default is still the default:
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := publicServer.Get(ctx, publicv1.ClusterVersionsGetRequest_builder{
				Id: defaultCv.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetIsDefault()).To(BeTrue())
		})
	})
})
