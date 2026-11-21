/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions and limitations under the
License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The operator creates a cluster's hosts one at a time through the fulfillment API, with creator
// system, in the cluster's project, linked through status.cluster. The cluster's admission
// already counted the whole node set, so each host create converts one asked host into one
// existing host and must add nothing: an admitted cluster must be able to fulfill at a full
// tenant. The disk volumes of a VM follow the same rule, linked through their ci-<vm id>- name.
// Only the system's creates net: a user's create, a host beyond the parent's ask, a parent that
// lost its ask or never existed, and a host of one node set netting against another node set's
// room all add their full price.
var _ = Describe("Quota netting of created children", func() {
	var volumes *PrivateVolumesServer

	BeforeEach(func() {
		var err error
		volumes, err = NewPrivateVolumesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetTierResolver(quotaTierResolver).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	// nettingRefusal is the refusal one world expects.
	type nettingRefusal struct {
		dimension string
		usage     int64
		added     int64
	}

	// nettingWorld is one seeded world of the table: the parent the child is created
	// for, the children that already exist, the tenant's limits, who creates the
	// child, and the refusal the create must end in, if any.
	type nettingWorld struct {
		// clusterSpec seeds the cluster row the created host links to through
		// status.cluster; empty seeds no cluster, so the host's parent is missing.
		clusterSpec string
		// vmSpec seeds a VM whose boot-disk volume the system creates, linked
		// through its ci-<vm id>-boot name; empty creates a host instead.
		vmSpec string
		// hostTypes are the host type rows the cluster's node sets name.
		hostTypes []string
		// existingHosts are host rows that already exist, linked to the cluster.
		existingHosts []string
		// limits are the tenant's limits by dimension.
		limits map[string]int64
		// userCreate makes a user, not the system, create the child.
		userCreate bool
		// refusal is the dimension, usage and added of the expected refusal; nil
		// when the create must pass.
		refusal *nettingRefusal
	}

	// createHost creates one host of the seeded type, linked to the cluster. A user's
	// create must never net against the parent's ask, or a user could claim an
	// admitted parent and get room for free, so the user entry creates through a
	// server whose attribution names a user.
	createHost := func(world nettingWorld) error {
		createAs := attribution
		if world.userCreate {
			user := auth.NewMockAttributionLogic(ctrl)
			user.EXPECT().DetermineAssignedCreator(gomock.Any()).
				Return("a-user", nil).AnyTimes()
			createAs = user
		}
		instances, err := NewGenericServer[*privatev1.BareMetalInstance]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstances_ServiceDesc.ServiceName).
			SetAttributionLogic(createAs).
			SetTenancyLogic(tenancy).
			Build()
		if err != nil {
			return err
		}
		var response *privatev1.BareMetalInstancesCreateResponse
		return instances.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{Name: "created-host", Tenant: testTenant}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					InstanceType: &privatev1.BareMetalInstanceTypeReference{Name: "metal-1tb"},
				}.Build(),
				Status: privatev1.BareMetalInstanceStatus_builder{
					Cluster: &privatev1.ClusterLocalReference{Id: "cluster-1"},
				}.Build(),
			}.Build(),
		}.Build(), &response)
	}

	DescribeTable("nets only the system's creates for an admitted parent, and nets nothing else",
		func(world nettingWorld) {
			for _, hostType := range world.hostTypes {
				insertRow("bare_metal_instance_types", hostType, auth.SharedTenant, "", `{}`)
			}
			for dimension, value := range world.limits {
				insertQuotaLimit(testTenant, "", dimension, value)
			}

			parentID := "cluster-1"
			if world.vmSpec != "" {
				parentID = uuid.New()
				insertRow("instance_types", "it-small", auth.SharedTenant, "",
					`{"spec":{"vcpus":2,"memory_gib":4}}`)
				insertRow("compute_instances", parentID, testTenant, "", world.vmSpec)
			} else if world.clusterSpec != "" {
				insertRow("clusters", "cluster-1", testTenant, "", world.clusterSpec)
			}
			for _, name := range world.existingHosts {
				insertRow("bare_metal_instances", name, testTenant, "",
					`{"spec":{"instance_type":{"name":"metal-1tb"}},
					  "status":{"cluster":{"id":"cluster-1"}}}`)
			}

			var err error
			if world.vmSpec != "" {
				_, err = volumes.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "ci-" + parentID + "-boot", Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "fast",
							SizeGib:     10,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			} else {
				err = createHost(world)
			}

			if world.refusal == nil {
				Expect(err).To(Succeed())
				return
			}
			_, failure := quotaFailure(err)
			Expect(failure.GetDimension()).To(Equal(world.refusal.dimension))
			Expect(failure.GetUsage()).To(Equal(world.refusal.usage))
			Expect(failure.GetAdded()).To(Equal(world.refusal.added))
		},

		Entry("the first host of an admitted cluster at a full tenant",
			nettingWorld{
				clusterSpec: `{"spec":{"node_sets":{"workers":{
					"baremetal_instance_type":{"name":"metal-1tb"},"size":5}}}}`,
				hostTypes: []string{"metal-1tb"},
				limits:    map[string]int64{"bare_metal.instances.metal-1tb": 5},
			},
		),
		Entry("the last host of an admitted cluster at a full tenant",
			nettingWorld{
				clusterSpec: `{"spec":{"node_sets":{"workers":{
					"baremetal_instance_type":{"name":"metal-1tb"},"size":5}}}}`,
				hostTypes:     []string{"metal-1tb"},
				existingHosts: []string{"host-1", "host-2", "host-3", "host-4"},
				limits:        map[string]int64{"bare_metal.instances.metal-1tb": 5},
			},
		),
		Entry("the boot-disk volume of an admitted VM at a full tenant",
			nettingWorld{
				vmSpec: `{"spec":{"instance_type":{"name":"it-small"},
					"boot_disk":{"size_gib":10,"storage_tier":{"name":"fast"}}}}`,
				limits: map[string]int64{"storage.volumes": 1, "storage.fast.gib": 10},
			},
		),
		Entry("a host beyond the cluster's ask, which adds its full price",
			nettingWorld{
				clusterSpec: `{"spec":{"node_sets":{"workers":{
					"baremetal_instance_type":{"name":"metal-1tb"},"size":5}}}}`,
				hostTypes:     []string{"metal-1tb"},
				existingHosts: []string{"host-1", "host-2", "host-3", "host-4", "host-5"},
				limits:        map[string]int64{"bare_metal.instances.metal-1tb": 5},
				refusal: &nettingRefusal{
					dimension: "bare_metal.instances.metal-1tb", usage: 5, added: 1,
				},
			},
		),
		Entry("a host of a missing parent, which adds its full price",
			nettingWorld{
				// No cluster row: the host names a parent that reserves no room, so
				// at a limit of 0 the full price lands. Had the missing parent
				// netted, the create would add nothing and fit even at 0.
				hostTypes: []string{"metal-1tb"},
				limits:    map[string]int64{"bare_metal.instances.metal-1tb": 0},
				refusal: &nettingRefusal{
					dimension: "bare_metal.instances.metal-1tb", usage: 0, added: 1,
				},
			},
		),
		Entry("a user's create, which adds its full price",
			nettingWorld{
				clusterSpec: `{"spec":{"node_sets":{"workers":{
					"baremetal_instance_type":{"name":"metal-1tb"},"size":5}}}}`,
				hostTypes:  []string{"metal-1tb"},
				limits:     map[string]int64{"bare_metal.instances.metal-1tb": 5},
				userCreate: true,
				refusal: &nettingRefusal{
					dimension: "bare_metal.instances.metal-1tb", usage: 5, added: 1,
				},
			},
		),
		Entry("a host after the cluster's node set was removed, which leaves no room",
			nettingWorld{
				clusterSpec:   `{"spec":{"node_sets":{}}}`,
				hostTypes:     []string{"metal-1tb"},
				existingHosts: []string{"host-1", "host-2", "host-3", "host-4", "host-5"},
				limits:        map[string]int64{"bare_metal.instances.metal-1tb": 5},
				refusal: &nettingRefusal{
					dimension: "bare_metal.instances.metal-1tb", usage: 5, added: 1,
				},
			},
		),
		Entry("a host of a full node set, which does not net against another node set's room",
			nettingWorld{
				clusterSpec: `{"spec":{"node_sets":{
					"workers":{"baremetal_instance_type":{"name":"metal-1tb"},"size":5},
					"accelerators":{"baremetal_instance_type":{"name":"metal-gpu"},"size":5}}}}`,
				hostTypes:     []string{"metal-1tb", "metal-gpu"},
				existingHosts: []string{"host-1", "host-2", "host-3", "host-4", "host-5"},
				limits: map[string]int64{
					"bare_metal.instances.metal-1tb": 5,
					"bare_metal.instances.metal-gpu": 5,
				},
				refusal: &nettingRefusal{
					dimension: "bare_metal.instances.metal-1tb", usage: 5, added: 1,
				},
			},
		),
	)
})
