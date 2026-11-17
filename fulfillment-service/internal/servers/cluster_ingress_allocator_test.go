package servers

import (
	"errors"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestSelectClusterIngressAddress(t *testing.T) {
	ipam := privatev1.SubnetIPAM_builder{
		GatewayIpv4:       "10.0.0.1",
		OverlayIpv4Cidrs:  []string{"10.0.0.0/25"},
		PhysicalIpv4Cidrs: []string{"10.0.0.128/26"},
		LbVipIpv4Cidrs:    []string{"10.0.0.0/24"},
	}.Build()

	got, err := selectClusterIngressAddress("10.0.0.0/24", ipam, map[string]struct{}{
		"10.0.0.192": {},
	})
	if err != nil {
		t.Fatalf("selectClusterIngressAddress() error = %v", err)
	}
	if got != "10.0.0.193" {
		t.Fatalf("selectClusterIngressAddress() = %q, want %q", got, "10.0.0.193")
	}
}

func TestSelectClusterIngressAddressExhausted(t *testing.T) {
	ipam := privatev1.SubnetIPAM_builder{
		GatewayIpv4:    "192.0.2.1",
		LbVipIpv4Cidrs: []string{"192.0.2.0/30"},
	}.Build()
	_, err := selectClusterIngressAddress("192.0.2.0/30", ipam, map[string]struct{}{
		"192.0.2.0": {},
		"192.0.2.1": {},
		"192.0.2.2": {},
		"192.0.2.3": {},
	})
	if !errors.Is(err, errClusterIngressAllocationExhausted) {
		t.Fatalf("selectClusterIngressAddress() error = %v, want exhaustion", err)
	}
}

func TestSelectClusterIngressAddressRejectsOutOfSubnetRange(t *testing.T) {
	ipam := privatev1.SubnetIPAM_builder{
		GatewayIpv4:    "192.0.2.1",
		LbVipIpv4Cidrs: []string{"198.51.100.0/24"},
	}.Build()
	if _, err := selectClusterIngressAddress("192.0.2.0/24", ipam, nil); err == nil {
		t.Fatal("selectClusterIngressAddress() succeeded for an out-of-Subnet range")
	}
}
