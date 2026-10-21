package provisioning_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// testBackendUsername and testBackendPassword are fixture values, not real
// credentials — used to verify connection details round-trip through the
// context helpers and AAP extra_vars conversion unmodified.
const (
	testBackendUsername = "test-backend-user"
	testBackendPassword = "test-backend-password"
)

var _ = Describe("ExtraVarsContext", func() {
	Describe("SubnetParentVirtualNetwork", func() {
		It("should round-trip the resolved parent identity", func() {
			ctx := context.Background()
			parent := provisioning.SubnetParentVirtualNetwork{
				FulfillmentID: "44444444-4444-4444-8444-444444444444",
				KubernetesUID: "55555555-5555-4555-8555-555555555555",
				TenantID:      "tenant-a",
				Phase:         "Ready",
			}

			ctx = provisioning.WithSubnetParentVirtualNetwork(ctx, parent)
			Expect(provisioning.SubnetParentVirtualNetworkFromContext(ctx)).To(Equal(parent))
		})

		It("should return the zero value when the parent identity is absent", func() {
			Expect(provisioning.SubnetParentVirtualNetworkFromContext(context.Background())).To(BeZero())
		})
	})

	Describe("ExternalIPPool", func() {
		It("round-trips the resolved pool identity", func() {
			pool := provisioning.ExternalIPPoolContext{
				FulfillmentID: "44444444-4444-4444-8444-444444444444",
				KubernetesUID: "55555555-5555-4555-8555-555555555555",
				TenantID:      "tenant-a",
				CIDR:          "198.51.100.0/24",
				Phase:         "Ready",
			}
			ctx := provisioning.WithExternalIPPool(context.Background(), pool)
			Expect(provisioning.ExternalIPPoolFromContext(ctx)).To(Equal(pool))
		})

		It("returns zero values when no pool context is present", func() {
			Expect(provisioning.ExternalIPPoolFromContext(context.Background())).To(BeZero())
		})
	})

	Describe("ExternalIPAttachmentContext", func() {
		It("round-trips the controller-resolved dependency snapshot", func() {
			value := provisioning.ExternalIPAttachmentContext{
				ExternalIP: provisioning.ExternalIPAttachmentExternalIPContext{
					Name: "public-ip", Namespace: "networking", FulfillmentID: "eip-id",
					KubernetesUID: "eip-uid", TenantID: "tenant-a", Address: "198.51.100.10",
					State: "Allocated", Phase: "Ready",
				},
				Target: provisioning.ExternalIPAttachmentTargetContext{
					Kind: "BareMetalInstance", Name: "machine", Namespace: "bmaas",
					FulfillmentID: "bmi-id", KubernetesUID: "bmi-uid", TenantID: "tenant-a",
					IPAddress: "10.0.0.10", Ready: true,
				},
				Subnet: provisioning.ExternalIPAttachmentSubnetContext{
					Name: "subnet", Namespace: "networking", FulfillmentID: "subnet-id",
					KubernetesUID: "subnet-uid", TenantID: "tenant-a", IPv4CIDR: "10.0.0.0/24",
					VirtualNetworkID: "vn-id", Phase: "Ready",
				},
				VirtualNetwork: provisioning.ExternalIPAttachmentVirtualNetworkContext{
					Name: "network", Namespace: "networking", FulfillmentID: "vn-id",
					KubernetesUID: "vn-uid", TenantID: "tenant-a", Phase: "Ready",
				},
			}
			ctx := provisioning.WithExternalIPAttachmentContext(context.Background(), value)
			Expect(provisioning.ExternalIPAttachmentContextFromContext(ctx)).To(Equal(value))
		})

		It("returns a zero value when attachment context is absent", func() {
			Expect(provisioning.ExternalIPAttachmentContextFromContext(context.Background())).To(BeZero())
		})
	})

	Describe("AdminKubeconfig", func() {
		It("should round-trip a kubeconfig value", func() {
			ctx := context.Background()
			kubeconfig := "apiVersion: v1\nclusters:\n- cluster:\n    server: https://example.com\n"

			ctx = provisioning.WithAdminKubeconfig(ctx, kubeconfig)
			result := provisioning.AdminKubeconfigFromContext(ctx)

			Expect(result).To(Equal(kubeconfig))
		})

		It("should return empty string from a context without kubeconfig", func() {
			ctx := context.Background()

			result := provisioning.AdminKubeconfigFromContext(ctx)

			Expect(result).To(BeEmpty())
		})
	})

	Describe("AddOnOperatorName", func() {
		It("should round-trip an operator name", func() {
			ctx := provisioning.WithAddOnOperatorName(context.Background(), "cert-manager")

			Expect(provisioning.AddOnOperatorNameFromContext(ctx)).To(Equal("cert-manager"))
		})

		It("should return empty string from a context without an operator name", func() {
			Expect(provisioning.AddOnOperatorNameFromContext(context.Background())).To(BeEmpty())
		})
	})

	Describe("StorageTierDefinitions", func() {
		It("should round-trip tier definitions", func() {
			ctx := context.Background()
			tiers := []provisioning.TierDefinition{
				{
					Name:      "fast",
					Protocol:  "nfs",
					Provider:  "vast",
					BackendID: "backend-1",
					QosLimits: provisioning.TierQosLimits{MaxReadBandwidthMBs: 100, MaxWriteBandwidthMBs: 200},
				},
			}

			ctx = provisioning.WithStorageTierDefinitions(ctx, tiers)
			result := provisioning.StorageTierDefinitionsFromContext(ctx)

			Expect(result).To(Equal(tiers))
		})

		It("should return nil from a context without tier definitions", func() {
			ctx := context.Background()

			result := provisioning.StorageTierDefinitionsFromContext(ctx)

			Expect(result).To(BeNil())
		})
	})

	Describe("StorageBackendConnections", func() {
		It("should round-trip backend connections", func() {
			ctx := context.Background()
			conns := map[string]provisioning.BackendConnection{
				"backend-1": {Endpoint: "https://vast.example.com", Username: testBackendUsername, Password: testBackendPassword},
			}

			ctx = provisioning.WithStorageBackendConnections(ctx, conns)
			result := provisioning.StorageBackendConnectionsFromContext(ctx)

			Expect(result).To(Equal(conns))
		})

		It("should return nil from a context without backend connections", func() {
			ctx := context.Background()

			result := provisioning.StorageBackendConnectionsFromContext(ctx)

			Expect(result).To(BeNil())
		})
	})

	Describe("AAPExtraVars", func() {
		It("should round-trip additional top-level AAP variables", func() {
			vars := map[string]any{
				"ansible_eda": map[string]any{"event": map[string]any{"payload": map[string]any{"kind": "ServerCluster"}}},
			}
			ctx := provisioning.WithAAPExtraVars(context.Background(), vars)

			Expect(provisioning.AAPExtraVarsFromContext(ctx)).To(Equal(vars))
		})

		It("should return nil when no extra variables are set", func() {
			Expect(provisioning.AAPExtraVarsFromContext(context.Background())).To(BeNil())
		})
	})
})
