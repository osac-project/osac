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
})
