package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

func readyConditions(status metav1.ConditionStatus) []metav1.Condition {
	return []metav1.Condition{{Type: v1alpha1.NetworkConditionReady, Status: status, Reason: v1alpha1.ReasonSucceeded}}
}

func TestBuildNATGatewayContext(t *testing.T) {
	const (
		tenantID      = "tenant-a"
		gatewayID     = "11111111-1111-4111-8111-111111111111"
		externalIPID  = "22222222-2222-4222-8222-222222222222"
		virtualNetID  = "33333333-3333-4333-8333-333333333333"
		externalIPUID = "44444444-4444-4444-8444-444444444444"
		virtualNetUID = "55555555-5555-4555-8555-555555555555"
	)

	newFixtures := func() (*v1alpha1.NATGateway, *v1alpha1.VirtualNetwork, *v1alpha1.ExternalIP) {
		gateway := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nat", Namespace: "networking", UID: types.UID(gatewayID),
				Labels:      map[string]string{osacNATGatewayIDLabel: gatewayID},
				Annotations: map[string]string{networktask.TenantAnnotation: tenantID},
			},
			Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: virtualNetID, ExternalIP: externalIPID},
		}
		virtualNetwork := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: "network", Namespace: "networking", UID: types.UID(virtualNetUID),
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetID},
				Annotations: map[string]string{networktask.TenantAnnotation: tenantID},
			},
			Spec:   v1alpha1.VirtualNetworkSpec{IPv4CIDR: "10.0.0.0/16"},
			Status: v1alpha1.VirtualNetworkStatus{Conditions: readyConditions(metav1.ConditionTrue)},
		}
		externalIP := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "egress-ip", Namespace: "networking", UID: types.UID(externalIPUID),
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{networktask.TenantAnnotation: tenantID},
			},
			Status: v1alpha1.ExternalIPStatus{
				Conditions: readyConditions(metav1.ConditionTrue),
				Address:    "198.51.100.10",
			},
		}
		return gateway, virtualNetwork, externalIP
	}

	t.Run("builds the complete current dependency snapshot", func(t *testing.T) {
		gateway, virtualNetwork, externalIP := newFixtures()
		got, pending, err := buildNATGatewayContext(gateway, virtualNetwork, externalIP)
		if err != nil || pending {
			t.Fatalf("buildNATGatewayContext() = (%+v, %v, %v)", got, pending, err)
		}
		want := natGatewayContext{
			ExternalIP: natGatewayExternalIPContext{
				Name: externalIP.Name, Namespace: externalIP.Namespace,
				FulfillmentID: externalIP.Labels[osacExternalIPIDLabel],
				KubernetesUID: string(externalIP.UID), TenantID: tenantID,
				Address: externalIP.Status.Address, State: "Allocated", Phase: "Ready",
			},
			VirtualNetwork: natGatewayVirtualNetworkContext{
				Name: virtualNetwork.Name, Namespace: virtualNetwork.Namespace,
				FulfillmentID: virtualNetwork.Labels[osacVirtualNetworkIDLabel],
				KubernetesUID: string(virtualNetwork.UID), TenantID: tenantID,
				IPv4CIDR: virtualNetwork.Spec.IPv4CIDR, Phase: "Ready",
			},
		}
		if got != want {
			t.Fatalf("got context %+v, want %+v", got, want)
		}
	})

	t.Run("waits for dependencies that are not Ready or allocated", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(*v1alpha1.NATGateway, *v1alpha1.VirtualNetwork, *v1alpha1.ExternalIP)
		}{
			{name: "virtual network not Ready", change: func(_ *v1alpha1.NATGateway, network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.Status.Conditions = readyConditions(metav1.ConditionFalse)
			}},
			{name: "external IP not Ready", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Conditions = readyConditions(metav1.ConditionFalse)
			}},
			{name: "external IP address", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Address = ""
			}},
			{name: "virtual network deleting", change: func(_ *v1alpha1.NATGateway, network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			}},
			{name: "NATGateway deleting", change: func(gateway *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				gateway.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			}},
			{name: "external IP deleting", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gateway, virtualNetwork, externalIP := newFixtures()
				tc.change(gateway, virtualNetwork, externalIP)
				_, pending, err := buildNATGatewayContext(gateway, virtualNetwork, externalIP)
				if err != nil || !pending {
					t.Fatalf("buildNATGatewayContext() pending=%v, err=%v; want pending", pending, err)
				}
			})
		}
	})

	t.Run("rejects mismatched identity or tenant", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(*v1alpha1.NATGateway, *v1alpha1.VirtualNetwork, *v1alpha1.ExternalIP)
		}{
			{name: "virtual network reference", change: func(gateway *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				gateway.Spec.VirtualNetwork = "other"
			}},
			{name: "external IP reference", change: func(gateway *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				gateway.Spec.ExternalIP = "other"
			}},
			{name: "virtual network tenant", change: func(_ *v1alpha1.NATGateway, network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.Annotations[networktask.TenantAnnotation] = "tenant-b"
			}},
			{name: "external IP tenant", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Annotations[networktask.TenantAnnotation] = "tenant-b"
			}},
			{name: "external IP namespace", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Namespace = "other"
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gateway, virtualNetwork, externalIP := newFixtures()
				tc.change(gateway, virtualNetwork, externalIP)
				if _, _, err := buildNATGatewayContext(gateway, virtualNetwork, externalIP); err == nil {
					t.Fatal("expected mismatched identity to be rejected")
				}
			})
		}
	})

	t.Run("rejects invalid canonical IPv4 inputs", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(*v1alpha1.VirtualNetwork, *v1alpha1.ExternalIP)
		}{
			{name: "IPv6 address", change: func(_ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Address = "2001:db8::1"
			}},
			{name: "noncanonical address", change: func(_ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Address = "198.051.100.10"
			}},
			{name: "noncanonical CIDR", change: func(network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.Spec.IPv4CIDR = "10.0.1.1/16"
			}},
			{name: "IPv6 CIDR", change: func(network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.Spec.IPv4CIDR = "2001:db8::/64"
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gateway, virtualNetwork, externalIP := newFixtures()
				tc.change(virtualNetwork, externalIP)
				if _, _, err := buildNATGatewayContext(gateway, virtualNetwork, externalIP); err == nil {
					t.Fatal("expected invalid IPv4 input to be rejected")
				}
			})
		}
	})
}

func TestNATGatewayContextVarsRoundTrip(t *testing.T) {
	value := natGatewayContext{
		ExternalIP:     natGatewayExternalIPContext{Name: "egress-ip", Address: "198.51.100.10", State: "Allocated", Phase: "Ready"},
		VirtualNetwork: natGatewayVirtualNetworkContext{Name: "network", IPv4CIDR: "10.0.0.0/16", Phase: "Ready"},
	}
	vars, err := natGatewayContextVars(value)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := decodeNATGatewaySubmitted(map[string]any{
		"resource":             map[string]any{"metadata": map[string]any{"generation": float64(3)}},
		inputNATGatewayContext: vars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Context != value || submitted.Resource.Metadata.Generation != 3 {
		t.Fatalf("got %+v, want context %+v and generation 3", submitted, value)
	}
}
