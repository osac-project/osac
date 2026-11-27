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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func resourceFixtureName() string {
	return "it-fixture-" + uuid.New()[24:]
}

// Cleanup waits for archival before removing dependencies. The harness reuses its Kind cluster.
func deferResourceFixtureDeletion(deleteDependency func(context.Context) error, deleted func(context.Context) (bool, error)) {
	GinkgoHelper()
	DeferCleanup(func(ctx context.Context) {
		err := deleteDependency(ctx)
		if status.Code(err) == codes.NotFound {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		if deleted != nil {
			Eventually(func() (bool, error) {
				removed, err := deleted(ctx)
				if status.Code(err) == codes.NotFound {
					return true, nil
				}
				return removed, err
			}, 5*time.Minute, 200*time.Millisecond).WithContext(ctx).Should(BeTrue(), "fixture was not removed")
		}
	})
}

// Observe archival frequently without increasing reconciliation signals. Each
// dependency gets its own probe, so cleanup still proceeds in dependency order.
func resourceDeletionProbe(get, signal func(context.Context) error) func(context.Context) (bool, error) {
	var nextSignal time.Time
	return func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		err := get(ctx)
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if time.Now().Before(nextSignal) {
			return false, nil
		}
		err = signal(ctx)
		// Throttle signals separately from the faster archival polling.
		nextSignal = time.Now().Add(2 * time.Second)
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, err
	}
}

func resourceFixtureMetadata(tenant, project string) *privatev1.Metadata {
	return privatev1.Metadata_builder{Name: resourceFixtureName(), Tenant: tenant, Project: project}.Build()
}

func unmanagedResourceFixtureMetadata(tenant, project string) *privatev1.Metadata {
	metadata := resourceFixtureMetadata(tenant, project)
	metadata.SetAnnotations(map[string]string{"osac.openshift.io/management-state": "unmanaged"})
	return metadata
}

func createDiskImageFixture(ctx context.Context, tenant, name string) *privatev1.DiskImage {
	GinkgoHelper()
	return createDiskImageInProjectFixture(ctx, tenant, "", name)
}

func createDiskImageInProjectFixture(ctx context.Context, tenant, project, name string) *privatev1.DiskImage {
	GinkgoHelper()
	client := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
		Object: privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant, Project: project}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/containerdisks/fedora:41",
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	object := response.GetObject()
	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: object.GetId()}.Build())
		return err
	}, nil)
	return object
}

func createComputeInstanceTypeFixture(ctx context.Context) string {
	GinkgoHelper()
	client := privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
		Object: privatev1.InstanceType_builder{
			Metadata: resourceFixtureMetadata("shared", ""),
			Spec:     privatev1.InstanceTypeSpec_builder{Vcpus: 2, MemoryGib: 4}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

type resourceNetworkFixture struct {
	subnetID, securityGroupID, virtualNetworkID, networkClassID string
}

func createNetworkClassFixture(ctx context.Context) string {
	GinkgoHelper()
	// The NetworkClass is the installer's deployment singleton: the fixture never deletes it.
	return createNetworkClassFixtureWithoutCleanup(ctx)
}

func createNetworkClassFixtureWithoutCleanup(ctx context.Context) string {
	GinkgoHelper()
	classes := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
	class, err := sharedNetworkClass(ctx, classes)
	Expect(err).NotTo(HaveOccurred())
	classID := class.GetObject().GetId()
	ensureNetworkClassFixtureReady(ctx, classes, classID)
	return classID
}

// ensureNetworkClassFixtureReady normalizes the deployment singleton for
// test fixtures that need to create tenant default networking. Preserve an
// existing canonical Hub; assign the integration suite's Hub when none exists.
func ensureNetworkClassFixtureReady(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
) {
	GinkgoHelper()
	response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
	Expect(err).NotTo(HaveOccurred())
	object := response.GetObject()
	if object.GetStatus().GetState() != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY {
		if !object.HasStatus() {
			object.SetStatus(&privatev1.NetworkClassStatus{})
		}
		object.GetStatus().SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
		object.GetStatus().ClearMessage()
		_, err = client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
			Object: object,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.state", "status.message"},
			},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
	}
	if object.GetStatus().GetHub() == "" {
		setNetworkClassCanonicalHub(ctx, client, id, hubId)
	}
	waitForNetworkClassReady(ctx, client, id)
}

func createNetworkFixture(ctx context.Context, tenant, project string) resourceNetworkFixture {
	GinkgoHelper()
	return createNetworkInClassFixture(ctx, tenant, project, createNetworkClassFixture(ctx))
}

func createSubnetInClassFixture(ctx context.Context, tenant, project, classID string) resourceNetworkFixture {
	GinkgoHelper()

	networks := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
	network, err := networks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
		Object: privatev1.VirtualNetwork_builder{
			Metadata: unmanagedResourceFixtureMetadata(tenant, project),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new("10.100.0.0/16"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	networkID := network.GetObject().GetId()

	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := networks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: networkID}.Build())
		return err
	}, resourceDeletionProbe(func(ctx context.Context) error {
		_, err := networks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		return err
	}, func(ctx context.Context) error {
		_, err := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.VirtualNetworksSignalRequest_builder{Id: networkID}.Build())
		return err
	}))

	Eventually(func() privatev1.VirtualNetworkState {
		r, e := networks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	// Wait for the initial projection before simulating provider readiness. Otherwise,
	// the first Kubernetes status sync can overwrite the READY update with PENDING.
	waitForNetworkingCR(ctx, listNetworkingCR(
		tool.KubeClient(),
		labels.VirtualNetworkUuid,
		networkID,
		&osacv1alpha1.VirtualNetworkList{},
		func(objects *osacv1alpha1.VirtualNetworkList) int { return len(objects.Items) },
	))
	waitForNetworkingFixtureResource(ctx, func(probeCtx context.Context) error {
		_, err := networks.Get(probeCtx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		return err
	})
	expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
		response, getErr := networks.Get(getCtx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		if getErr != nil {
			return "", getErr
		}
		return response.GetObject().GetStatus().GetHub(), nil
	})
	setRoutingVirtualNetworkReady(ctx, networks, networkID)

	subnets := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
	subnet, err := subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
		Object: privatev1.Subnet_builder{
			Metadata: unmanagedResourceFixtureMetadata(tenant, project),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: networkID}.Build(),
				Ipv4Cidr:       new("10.100.1.0/24"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	subnetID := subnet.GetObject().GetId()

	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
		return err
	}, resourceDeletionProbe(func(ctx context.Context) error {
		_, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		return err
	}, func(ctx context.Context) error {
		_, err := privatev1.NewSubnetsClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.SubnetsSignalRequest_builder{Id: subnetID}.Build())
		return err
	}))

	Eventually(func() privatev1.SubnetState {
		r, e := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
	waitForNetworkingCR(ctx, listNetworkingCR(
		tool.KubeClient(),
		labels.SubnetUuid,
		subnetID,
		&osacv1alpha1.SubnetList{},
		func(objects *osacv1alpha1.SubnetList) int { return len(objects.Items) },
	))
	waitForNetworkingFixtureResource(ctx, func(probeCtx context.Context) error {
		_, err := subnets.Get(probeCtx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		return err
	})
	expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
		response, getErr := subnets.Get(getCtx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		if getErr != nil {
			return "", getErr
		}
		return response.GetObject().GetStatus().GetHub(), nil
	})
	setRoutingSubnetReady(ctx, subnets, subnetID)
	return resourceNetworkFixture{subnetID: subnetID, virtualNetworkID: networkID, networkClassID: classID}
}

func createNetworkInClassFixture(ctx context.Context, tenant, project, classID string) resourceNetworkFixture {
	GinkgoHelper()
	network := createSubnetInClassFixture(ctx, tenant, project, classID)

	groups := privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
	group, err := groups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
		Object: privatev1.SecurityGroup_builder{
			Metadata: unmanagedResourceFixtureMetadata(tenant, project),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: network.virtualNetworkID}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	groupID := group.GetObject().GetId()

	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := groups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: groupID}.Build())
		return err
	}, resourceDeletionProbe(func(ctx context.Context) error {
		_, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		return err
	}, func(ctx context.Context) error {
		_, err := privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.SecurityGroupsSignalRequest_builder{Id: groupID}.Build())
		return err
	}))

	waitForNetworkingFixtureResource(ctx, func(probeCtx context.Context) error {
		_, err := groups.Get(probeCtx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		return err
	})
	expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
		response, getErr := groups.Get(getCtx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		if getErr != nil {
			return "", getErr
		}
		return response.GetObject().GetStatus().GetHub(), nil
	})

	Eventually(func() privatev1.SecurityGroupState {
		r, e := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
	waitForNetworkingCR(ctx, listNetworkingCR(
		tool.KubeClient(),
		labels.SecurityGroupUuid,
		groupID,
		&osacv1alpha1.SecurityGroupList{},
		func(objects *osacv1alpha1.SecurityGroupList) int { return len(objects.Items) },
	))
	// The Security Group must be ready before it can join a resource attachment.
	currentSecurityGroup, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
	Expect(err).NotTo(HaveOccurred())
	currentSecurityGroup.GetObject().GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
	_, err = groups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{Object: currentSecurityGroup.GetObject(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}}}.Build())
	Expect(err).NotTo(HaveOccurred())
	network.securityGroupID = groupID
	return network
}

func (f resourceNetworkFixture) computeInstanceAttachment() *publicv1.ComputeNetworkAttachment {
	attachment := publicv1.ComputeNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func (f resourceNetworkFixture) bareMetalInstanceAttachment() *publicv1.BareMetalNetworkAttachment {
	attachment := publicv1.BareMetalNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func (f resourceNetworkFixture) clusterAttachment() *publicv1.ClusterNetworkAttachment {
	attachment := publicv1.ClusterNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func (f resourceNetworkFixture) privateClusterAttachment() *privatev1.ClusterNetworkAttachment {
	attachment := privatev1.ClusterNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func createComputeInstanceTemplateFixture(ctx context.Context, defaults *privatev1.ComputeInstanceTemplateSpecDefaults, parameters []*privatev1.ComputeInstanceTemplateParameterDefinition) string {
	GinkgoHelper()
	client := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
		Object: privatev1.ComputeInstanceTemplate_builder{
			Id:           "it_compute_instance_" + uuid.New()[24:],
			Metadata:     resourceFixtureMetadata("shared", ""),
			Title:        "Integration compute template",
			SpecDefaults: defaults,
			Parameters:   parameters,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferResourceFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}
