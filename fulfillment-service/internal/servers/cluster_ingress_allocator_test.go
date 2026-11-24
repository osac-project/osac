package servers

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("cluster ingress address allocation", func() {
	It("selects an available address from the load-balancer VIP range", func() {
		ipam := privatev1.SubnetIPAM_builder{
			GatewayIpv4:       "10.0.0.1",
			OverlayIpv4Cidrs:  []string{"10.0.0.0/25"},
			PhysicalIpv4Cidrs: []string{"10.0.0.128/26"},
			LbVipIpv4Cidrs:    []string{"10.0.0.0/24"},
		}.Build()

		got, err := selectClusterIngressAddress(context.Background(), "10.0.0.0/24", ipam, map[string]struct{}{
			"10.0.0.192": {},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("10.0.0.193"))
	})

	It("reports exhaustion when every address is allocated", func() {
		ipam := privatev1.SubnetIPAM_builder{
			GatewayIpv4:    "192.0.2.1",
			LbVipIpv4Cidrs: []string{"192.0.2.0/30"},
		}.Build()
		_, err := selectClusterIngressAddress(context.Background(), "192.0.2.0/30", ipam, map[string]struct{}{
			"192.0.2.0": {},
			"192.0.2.1": {},
			"192.0.2.2": {},
			"192.0.2.3": {},
		})
		Expect(errors.Is(err, errClusterIngressAllocationExhausted)).To(BeTrue(),
			"selectClusterIngressAddress() error = %v", err)
	})

	It("rejects an ingress range outside the subnet IPAM ranges", func() {
		ipam := privatev1.SubnetIPAM_builder{
			GatewayIpv4:    "192.0.2.1",
			LbVipIpv4Cidrs: []string{"198.51.100.0/24"},
		}.Build()
		_, err := selectClusterIngressAddress(context.Background(), "192.0.2.0/24", ipam, nil)
		Expect(err).To(HaveOccurred())
	})
})
