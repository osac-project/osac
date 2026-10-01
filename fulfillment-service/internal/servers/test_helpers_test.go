/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// stubHubClientProvider is a test double for HubClientProvider that returns a fixed client.
type stubHubClientProvider struct {
	client    clnt.Client
	config    *rest.Config
	namespace string
}

func (s *stubHubClientProvider) GetClient(_ context.Context, _ string) (*HubClientInfo, error) {
	return &HubClientInfo{
		Client:    s.client,
		Config:    s.config,
		Namespace: s.namespace,
	}, nil
}

func (s *stubHubClientProvider) EvictClient(_ string) {
	// No-op for stub
}

// hubClientProviderWithEvictionTracking is a test double for HubClientProvider that tracks EvictClient calls.
type hubClientProviderWithEvictionTracking struct {
	client        clnt.Client
	config        *rest.Config
	namespace     string
	evictedHubIDs []string
}

func (h *hubClientProviderWithEvictionTracking) GetClient(_ context.Context, _ string) (*HubClientInfo, error) {
	return &HubClientInfo{
		Client:    h.client,
		Config:    h.config,
		Namespace: h.namespace,
	}, nil
}

func (h *hubClientProviderWithEvictionTracking) EvictClient(hubID string) {
	h.evictedHubIDs = append(h.evictedHubIDs, hubID)
}

func createComputeInstanceInState(
	ctx context.Context,
	computeInstanceDao *dao.GenericDAO[*privatev1.ComputeInstance],
	state privatev1.ComputeInstanceState,
) *privatev1.ComputeInstance {
	resp, err := computeInstanceDao.Create().SetObject(
		privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "shared",
				Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
			}.Build(),
			Status: privatev1.ComputeInstanceStatus_builder{
				State: state,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

// createDiskImageWithLifecycle seeds a DiskImage with the given lifecycle directly through
// the DAO as a shared/global image, so a tenancy-filtered DAO resolves it. Id and Name are
// set to the same value so a string default resolves whether treated as an id or a name.
// Note: unit tests call servers directly, so the gRPC reference-validation interceptor is
// not in the chain — existence is exercised by the handler's own lookup.
func createDiskImageWithLifecycle(
	name string,
	lifecycle privatev1.DiskImageLifecycle,
	deprecation *privatev1.DiskImageDeprecation,
) {
	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	_, err = diskImagesDao.Create().SetObject(
		privatev1.DiskImage_builder{
			Id: name,
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				Lifecycle:   lifecycle,
				Deprecation: deprecation,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// createTenant seeds a Tenant row through the universal suite DAO. Non-shared tenants must exist
// before a DiskImage can reference them: the reverse-reference trigger rejects a disk image whose
// tenant is unknown.
func createTenant(id string) {
	tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
		SetLogger(logger).
		SetTableName("tenants").
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	_, err = tenantsDao.Create().SetObject(
		privatev1.Tenant_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Name:   id,
				Tenant: id,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// seedTenantDefaultNetworking seeds a READY default Subnet and SecurityGroup (plus backing
// VirtualNetwork) so BareMetalInstance Create can inject omitted attachments. Reuses the
// deployment-singleton NetworkClass when one already exists; otherwise creates one with the
// optional fabricManager.
func seedTenantDefaultNetworking(tenant, project string, fabricManager *string) (
	subnetID, securityGroupID, virtualNetworkID string,
) {
	ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ncList, err := ncDao.List().SetFilter("!has(this.metadata.deletion_timestamp)").SetLimit(1).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	var ncID string
	if items := ncList.GetItems(); len(items) > 0 {
		ncID = items[0].GetId()
	} else {
		ncResp, createErr := ncDao.Create().SetObject(privatev1.NetworkClass_builder{
			FabricManager: fabricManager,
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("default-nc-%s", uuid.NewString()[:8]),
				Tenant: tenant,
			}.Build(),
		}.Build()).Do(ctx)
		ExpectWithOffset(1, createErr).ToNot(HaveOccurred())
		ncID = ncResp.GetObject().GetId()
	}

	vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	vnResp, err := vnDao.Create().SetObject(privatev1.VirtualNetwork_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    fmt.Sprintf("default-vn-%s", uuid.NewString()[:8]),
			Tenant:  tenant,
			Project: project,
			Labels: map[string]string{
				"osac.openshift.io/default": "true",
			},
		}.Build(),
		Spec: privatev1.VirtualNetworkSpec_builder{
			NetworkClass: privatev1.NetworkClassReference_builder{Id: ncID}.Build(),
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	virtualNetworkID = vnResp.GetObject().GetId()

	subnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ipv4Cidr := "10.200.0.0/24"
	subnetResp, err := subnetDao.Create().SetObject(privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    fmt.Sprintf("default-subnet-%s", uuid.NewString()[:8]),
			Tenant:  tenant,
			Project: project,
			Labels: map[string]string{
				"osac.openshift.io/default": "true",
			},
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			Ipv4Cidr:       &ipv4Cidr,
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
		}.Build(),
		Status: privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	subnetID = subnetResp.GetObject().GetId()

	sgDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	sgResp, err := sgDao.Create().SetObject(privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    fmt.Sprintf("default-sg-%s", uuid.NewString()[:8]),
			Tenant:  tenant,
			Project: project,
			Labels: map[string]string{
				"osac.openshift.io/default": "true",
			},
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
		}.Build(),
		Status: privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	securityGroupID = sgResp.GetObject().GetId()
	return subnetID, securityGroupID, virtualNetworkID
}

// createAvailableDiskImageInTenant seeds an AVAILABLE DiskImage with an explicit id, name, and
// tenant through the universal suite DAO (an unfiltered write). Used by the name-collision
// precedence tests, where a shared image and one or more same-name tenant images must coexist so
// the resolver's own-tenant-then-shared tie-break can be exercised. Unlike
// createDiskImageWithLifecycle (which only seeds shared images with Id == Name), this decouples id
// from name so a by-id lookup can be distinguished from a by-name lookup.
func createAvailableDiskImageInTenant(id, name, tenant string) {
	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	_, err = diskImagesDao.Create().SetObject(
		privatev1.DiskImage_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: tenant,
			}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}
