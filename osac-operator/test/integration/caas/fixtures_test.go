/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package caas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func fixtureName(prefix string) string {
	var suffix [8]byte
	_, err := rand.Read(suffix[:])
	Expect(err).NotTo(HaveOccurred())
	return fmt.Sprintf("caas-%s-%s", prefix, hex.EncodeToString(suffix[:]))
}

func createDiskImage(ctx context.Context) *privatev1.DiskImage {
	client := privatev1.NewDiskImagesClient(fulfillmentConn)
	response, err := client.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
		Object: privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{Name: fixtureName("image")}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/test/rhel9:latest",
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned disk image")
	image := response.GetObject()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred(), "delete test-owned disk image %s", image.GetId())
	})
	return image
}

func createClusterVersion(ctx context.Context, image *privatev1.DiskImage) *privatev1.ClusterVersion {
	client := privatev1.NewClusterVersionsClient(fulfillmentConn)
	response, err := client.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
		Object: privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{Name: fixtureName("version")}.Build(),
			Spec: privatev1.ClusterVersionSpec_builder{
				Version:   "4.17.0-caas." + strings.TrimPrefix(fixtureName("version"), "caas-version-"),
				Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
				DiskImage: privatev1.DiskImageReference_builder{Id: image.GetId()}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned disk-image-backed cluster version")
	version := response.GetObject()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{Id: version.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred(), "delete test-owned cluster version %s", version.GetId())
	})
	return version
}

const (
	caasSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"
	workerTemplateID = "osac.templates.bm_host_provisioning"
)

func ensureWorkerTemplate(ctx context.Context) {
	client := privatev1.NewBareMetalInstanceTemplatesClient(fulfillmentConn)
	response, err := client.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: workerTemplateID}.Build())
	var object *privatev1.BareMetalInstanceTemplate
	if status.Code(err) == codes.NotFound {
		created, err := client.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:       workerTemplateID,
				Metadata: privatev1.Metadata_builder{Name: fixtureName("worker-template"), Tenant: "shared"}.Build(),
				Title:    "Connected CaaS worker BMI template",
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred(), "create test-owned shared worker BMI template")
		object = created.GetObject()
		DeferCleanup(func(ctx context.Context) {
			_, err := client.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{Id: workerTemplateID}.Build())
			Expect(err).NotTo(HaveOccurred(), "delete test-owned worker BMI template")
		})
	} else {
		Expect(err).NotTo(HaveOccurred(), "read existing worker BMI template")
		object = response.GetObject()
	}
	Expect(object.GetMetadata().GetTenant()).To(Equal("shared"))
}

const simTenantName = "caas-connected-sim"

// verifySimDefaultFabricManager checks the Helm-created singleton through the real API.
// Kind simulates readiness, not CUDN provisioning; never rewrite an existing class.
func verifySimDefaultFabricManager(ctx context.Context, vn *privatev1.VirtualNetwork) string {
	Expect(connectedConfig.clusterName).To(Equal("osac-sim"))
	Expect(vn.GetMetadata().GetTenant()).To(Equal(simTenantName))
	Expect(vn.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
	classID := vn.GetSpec().GetNetworkClass().GetId()
	Expect(classID).NotTo(BeEmpty(), "default VN must reference a NetworkClass by ID")
	client := privatev1.NewNetworkClassesClient(fulfillmentConn)
	response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: classID}.Build())
	Expect(err).NotTo(HaveOccurred(), "read marked VN's NetworkClass")
	class := response.GetObject()
	Expect(class.GetIsDefault()).To(BeTrue())
	Expect(class.GetFabricManager()).To(Equal("cudn_net"), "Helm must install CUDN as the default fabric manager")
	Expect(class.GetK8SManager()).To(BeEmpty(), "CUDN must not be paired with a k8s manager")
	return classID
}

// advanceDefaultNetworking simulates ONLY the external networking controller's
// readiness feedback. It keeps the real service API, DB, tenant and protected
// default resources; no fake subnet or security group is created. Like the
// fulfillment-service default-networking IT, it advances VN -> subnet -> SG.
func advanceDefaultNetworking(ctx context.Context) {
	tenant := ensureSimTenant(ctx)
	Expect(tenant).To(Equal(simTenantName))
	filter := fmt.Sprintf(
		`this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q`, tenant)
	mask := &fieldmaskpb.FieldMask{Paths: []string{"status.state"}}

	vns := privatev1.NewVirtualNetworksClient(fulfillmentConn)
	var vn *privatev1.VirtualNetwork
	Eventually(func(g Gomega) {
		response, err := vns.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(response.GetItems()).To(HaveLen(1), "only this tenant's marked default VN may be advanced")
		vn = response.GetItems()[0]
	}, 45*time.Second, time.Second).Should(Succeed())
	verifySimDefaultFabricManager(ctx, vn)
	if vn.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		Expect(vn.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		vn.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err := vns.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{Object: vn, UpdateMask: mask}.Build())
		Expect(err).NotTo(HaveOccurred(), "simulate readiness of marked default VN")
	}

	subnets := privatev1.NewSubnetsClient(fulfillmentConn)
	var subnet *privatev1.Subnet
	Eventually(func(g Gomega) {
		response, err := subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(response.GetItems()).To(HaveLen(1), "only this tenant's marked default subnet may be advanced")
		subnet = response.GetItems()[0]
	}, 45*time.Second, time.Second).Should(Succeed())
	Expect(subnet.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()),
		"default subnet must belong to the CUDN default VN")
	if subnet.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
		Expect(subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		subnet.SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build())
		_, err := subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{Object: subnet, UpdateMask: mask}.Build())
		Expect(err).NotTo(HaveOccurred(), "simulate readiness of marked default subnet")
	}

	groups := privatev1.NewSecurityGroupsClient(fulfillmentConn)
	var group *privatev1.SecurityGroup
	Eventually(func(g Gomega) {
		response, err := groups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(response.GetItems()).To(HaveLen(1), "only this tenant's marked default security group may be advanced")
		group = response.GetItems()[0]
	}, 45*time.Second, time.Second).Should(Succeed())
	if group.GetStatus().GetState() != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY {
		Expect(group.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		group.SetStatus(privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
		}.Build())
		_, err := groups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{Object: group, UpdateMask: mask}.Build())
		Expect(err).NotTo(HaveOccurred(), "simulate readiness of marked default security group")
	}
}

func ensureSimTenant(ctx context.Context) string {
	// The dedicated Kind backend retains this one marked tenant and its protected
	// default networking until sim-down, rather than leaking one tenant per spec.
	client := privatev1.NewTenantsClient(fulfillmentConn)
	filter := fmt.Sprintf("this.metadata.name == %q", simTenantName)
	list, err := client.List(ctx, privatev1.TenantsListRequest_builder{Filter: &filter}.Build())
	Expect(err).NotTo(HaveOccurred(), "look up dedicated CaaS sim tenant")
	if len(list.GetItems()) == 0 {
		created, err := client.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{
				Name: simTenantName,
				Annotations: map[string]string{
					"osac.openshift.io/sim-owner":        simOwner,
					"osac.openshift.io/sim-cluster-name": connectedConfig.clusterName,
				},
			}.Build()}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred(), "create dedicated CaaS sim tenant")
		return created.GetObject().GetMetadata().GetName()
	}
	Expect(list.GetItems()).To(HaveLen(1), "one dedicated CaaS sim tenant must exist")
	metadata := list.GetItems()[0].GetMetadata()
	Expect(metadata.GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/sim-owner", simOwner),
		"refusing to reuse a tenant not owned by the dedicated CaaS sim")
	Expect(metadata.GetAnnotations()).To(HaveKeyWithValue(
		"osac.openshift.io/sim-cluster-name", connectedConfig.clusterName))
	Expect(metadata.HasDeletionTimestamp()).To(BeFalse(), "dedicated CaaS sim tenant is deleting")
	return metadata.GetName()
}

func createCaaSInstanceType(ctx context.Context) string {
	client := privatev1.NewBareMetalInstanceTypesClient(fulfillmentConn)
	name := fixtureName("bmit")
	response, err := client.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
		Object: privatev1.BareMetalInstanceType_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: "shared"}.Build(),
			Spec: privatev1.BareMetalInstanceTypeSpec_builder{
				Hardware: privatev1.BareMetalHardwareSpec_builder{
					Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 4, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
					Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 16}.Build(),
					NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
						privatev1.BareMetalNetworkPortSpec_builder{
							Name: "eth0", Role: "fabric", Type: "Ethernet", Speed: "10Gbps",
						}.Build(),
					},
				}.Build(),
				HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
					MatchLabels: map[string]string{"hardware.profile": "compute"},
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned shared bare-metal instance type")
	id := response.GetObject().GetId()
	got, err := fulfillmentClient.GetBareMetalInstanceType(ctx, id)
	Expect(err).NotTo(HaveOccurred(), "read test-owned BMIT through worker client")
	Expect(got.GetSpec().GetHardware().GetNetworkPorts()[0].GetRole()).To(Equal("fabric"))
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred(), "delete test-owned instance type %s", id)
	})
	return name
}

func createCaaSCluster(ctx context.Context) *privatev1.Cluster {
	return createCaaSClusterWithNodeSets(ctx, false)
}

func createCaaSClusterWithNodeSets(ctx context.Context, twoTypes bool) *privatev1.Cluster {
	// The private admin API is used to exercise storage/reconciliation, not public tenant authorization.
	tenantName := ensureSimTenant(ctx)
	// All cluster fixtures in this isolated backend use one tenant. Only the
	// tenant and its system-managed descendants survive the suite until sim-down.

	secretsClient := privatev1.NewSecretsClient(fulfillmentConn)
	secretResponse, err := secretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: fixtureName("pull-secret"), Tenant: tenantName}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
			Data: map[string][]byte{
				".dockerconfigjson": []byte(`{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			},
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned tenant pull secret")
	secretID := secretResponse.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: secretID}.Build())
		Expect(err).NotTo(HaveOccurred(), "delete test-owned pull secret %s", secretID)
	})

	image := createDiskImage(ctx)
	version := createClusterVersion(ctx, image)

	instanceName := createCaaSInstanceType(ctx)
	nodeSets := map[string]*privatev1.ClusterNodeSet{
		"compute": privatev1.ClusterNodeSet_builder{
			Size:                  new(int32(1)),
			BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: instanceName}.Build(),
		}.Build(),
	}
	if twoTypes {
		gpuType := createCaaSInstanceType(ctx)
		nodeSets["gpu"] = privatev1.ClusterNodeSet_builder{
			Size:                  new(int32(2)),
			BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: gpuType}.Build(),
		}.Build()
	}

	templates := privatev1.NewClusterTemplatesClient(fulfillmentConn)
	templateResponse, err := templates.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
		Object: privatev1.ClusterTemplate_builder{
			Id:       strings.ReplaceAll(fixtureName("template"), "-", "_"),
			Metadata: privatev1.Metadata_builder{Name: fixtureName("template"), Tenant: "shared"}.Build(),
			Title:    "Connected CaaS integration template",
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned shared cluster template")
	templateID := templateResponse.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := templates.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: templateID}.Build())
		Expect(err).NotTo(HaveOccurred(), "delete test-owned cluster template %s", templateID)
	})

	clusters := privatev1.NewClustersClient(fulfillmentConn)
	clusterResponse, err := clusters.Create(ctx, privatev1.ClustersCreateRequest_builder{
		Object: privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Name: fixtureName("cluster"), Tenant: tenantName}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template:         privatev1.ClusterTemplateReference_builder{Id: templateID}.Build(),
				Version:          privatev1.ClusterVersionReference_builder{Id: version.GetId()}.Build(),
				PullSecretSecret: privatev1.SecretLocalReference_builder{Id: secretID}.Build(),
				SshPublicKey:     new(caasSSHPublicKey),
				NodeSets:         nodeSets,
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "create test-owned Cluster in tenant %s", tenantName)
	cluster := clusterResponse.GetObject()
	DeferCleanup(func(ctx context.Context) {
		_, err := clusters.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: cluster.GetId()}.Build())
		Expect(status.Code(err)).To(BeElementOf(codes.OK, codes.NotFound),
			"delete test-owned cluster %s: %v", cluster.GetId(), err)
		Eventually(func(g Gomega) {
			_, err := clusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(status.Code(err)).To(Equal(codes.NotFound),
				"wait for ClusterOrder deletion and Cluster archiving before tenant teardown: %v", err)
		}, 90*time.Second, time.Second).Should(Succeed())
	})
	// LIFO cleanup: preserve API state before deleting the Cluster and its dependencies.
	DeferCleanup(func(ctx context.Context) {
		if CurrentSpecReport().Failed() {
			logFixtureDiagnostics(ctx, cluster)
		}
	})
	// The real controller persists the chosen hub before writing the order. A
	// deletion while hub is empty can strand its finalizer, so never tear down
	// a freshly created Cluster until this ownership boundary is committed.
	Eventually(func(g Gomega) {
		latest, err := fulfillmentClient.GetCluster(ctx, cluster.GetId())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(latest.GetStatus().GetHub()).NotTo(BeEmpty(),
			"cluster %s must select its hub before cleanup; state=%s conditions=%v",
			cluster.GetId(), latest.GetStatus().GetState(), latest.GetStatus().GetConditions())
	}, 60*time.Second, 500*time.Millisecond).Should(Succeed())
	return cluster
}

var _ = Describe("test-owned CaaS API fixtures", func() {
	It("reuses the same isolated sim tenant across successive Cluster fixtures", func(ctx context.Context) {
		first := createCaaSCluster(ctx)
		second := createCaaSCluster(ctx)
		Expect(first.GetMetadata().GetTenant()).To(Equal(second.GetMetadata().GetTenant()))
		Expect(first.GetId()).NotTo(Equal(second.GetId()))
	})

	It("creates a non-system tenant cluster with authoritative ownership and bare-metal nodes", func(ctx context.Context) {
		// Private-admin calls exercise persistence and reconciliation, NOT public tenant authorization.
		cluster := createCaaSCluster(ctx)
		Expect(cluster).NotTo(BeNil())
		got, err := fulfillmentClient.GetCluster(ctx, cluster.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetId()).To(Equal(cluster.GetId()))
		Expect(got.GetMetadata().GetTenant()).To(Equal(cluster.GetMetadata().GetTenant()))
		Expect(got.GetMetadata().GetTenant()).NotTo(Equal("system"))
		Expect(got.GetSpec().GetNodeSets()).To(HaveLen(1))
		Expect(got.GetSpec().GetPullSecretSecret()).NotTo(BeNil(), "cluster fixture must carry a valid pull secret")
	})
	It("rejects an invalid cluster template reference at fixture creation", func(ctx context.Context) {
		client := privatev1.NewClustersClient(fulfillmentConn)
		_, err := client.Create(ctx, privatev1.ClustersCreateRequest_builder{
			Object: privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Name: fixtureName("invalid"), Tenant: "missing-caas-tenant"}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: privatev1.ClusterTemplateReference_builder{Id: "missing-caas-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument),
			"invalid cluster fixture must fail at real API creation: %v", err)
		Expect(err.Error()).To(ContainSubstring("template 'missing-caas-template' not found"))
	})

	It("makes the fixed worker BMI template available to the real private API", func(ctx context.Context) {
		ensureWorkerTemplate(ctx)
		client := privatev1.NewBareMetalInstanceTemplatesClient(fulfillmentConn)
		response, err := client.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: workerTemplateID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal("shared"))
	})

	It("makes a disk image and cluster version readable by the production client", func(ctx context.Context) {
		image := createDiskImage(ctx)
		version := createClusterVersion(ctx, image)

		gotImage, err := fulfillmentClient.GetDiskImage(ctx, image.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(gotImage.GetId()).To(Equal(image.GetId()))

		gotVersion, err := fulfillmentClient.GetClusterVersion(ctx, version.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(gotVersion.GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))
	})
})
