package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

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
				Annotations: map[string]string{osacTenantKey: tenantID},
			},
			Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: virtualNetID, ExternalIP: externalIPID},
		}
		virtualNetwork := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: "network", Namespace: "networking", UID: types.UID(virtualNetUID),
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetID},
				Annotations: map[string]string{osacTenantKey: tenantID},
			},
			Spec:   v1alpha1.VirtualNetworkSpec{IPv4CIDR: "10.0.0.0/16"},
			Status: v1alpha1.VirtualNetworkStatus{Phase: v1alpha1.VirtualNetworkPhaseReady},
		}
		externalIP := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "egress-ip", Namespace: "networking", UID: types.UID(externalIPUID),
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: tenantID},
			},
			Status: v1alpha1.ExternalIPStatus{
				State:   v1alpha1.ExternalIPStateAllocated,
				Phase:   v1alpha1.ExternalIPPhaseReady,
				Address: "198.51.100.10",
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
		want := provisioning.NATGatewayContext{
			ExternalIP: provisioning.NATGatewayExternalIPContext{
				Name: externalIP.Name, Namespace: externalIP.Namespace,
				FulfillmentID: externalIP.Labels[osacExternalIPIDLabel],
				KubernetesUID: string(externalIP.UID), TenantID: tenantID,
				Address: externalIP.Status.Address, State: string(externalIP.Status.State),
				Phase: string(externalIP.Status.Phase),
			},
			VirtualNetwork: provisioning.NATGatewayVirtualNetworkContext{
				Name: virtualNetwork.Name, Namespace: virtualNetwork.Namespace,
				FulfillmentID: virtualNetwork.Labels[osacVirtualNetworkIDLabel],
				KubernetesUID: string(virtualNetwork.UID), TenantID: tenantID,
				IPv4CIDR: virtualNetwork.Spec.IPv4CIDR,
				Phase:    string(virtualNetwork.Status.Phase),
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
			{name: "virtual network phase", change: func(_ *v1alpha1.NATGateway, network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.Status.Phase = v1alpha1.VirtualNetworkPhaseProgressing
			}},
			{name: "external IP phase", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Phase = v1alpha1.ExternalIPPhaseProgressing
			}},
			{name: "external IP state", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.State = v1alpha1.ExternalIPStatePending
			}},
			{name: "external IP address", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Status.Address = ""
			}},
			{name: "virtual network deleting", change: func(_ *v1alpha1.NATGateway, network *v1alpha1.VirtualNetwork, _ *v1alpha1.ExternalIP) {
				network.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
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
				network.Annotations[osacTenantKey] = "tenant-b"
			}},
			{name: "external IP tenant", change: func(_ *v1alpha1.NATGateway, _ *v1alpha1.VirtualNetwork, eip *v1alpha1.ExternalIP) {
				eip.Annotations[osacTenantKey] = "tenant-b"
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

func TestResolveNATGatewayManagerHonorsPinnedContract(t *testing.T) {
	strategy, version, err := resolveNATGatewayManager(
		context.Background(), nil, "", "other-manager", "agentless_net",
		networkmanager.NATGatewayContractVersionV1,
	)
	if err != nil {
		t.Fatalf("resolveNATGatewayManager() error = %v", err)
	}
	if strategy != "agentless_net" || version != networkmanager.NATGatewayContractVersionV1 {
		t.Fatalf("resolveNATGatewayManager() = (%q, %q), want pinned strategy and v1", strategy, version)
	}

	if _, _, err := resolveNATGatewayManager(
		context.Background(), nil, "", "agentless_net", "",
		networkmanager.NATGatewayContractVersionV1,
	); !errors.Is(err, errNATGatewayContract) {
		t.Fatalf("resolveNATGatewayManager() error = %v, want errNATGatewayContract", err)
	}
}

func TestResolveNATGatewayManagerMarksContractErrors(t *testing.T) {
	if _, _, err := resolveNATGatewayManager(
		context.Background(), nil, "", "", "", "v2",
	); !errors.Is(err, errNATGatewayContract) {
		t.Fatalf("unsupported pinned version error = %v, want errNATGatewayContract", err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	managerConfig := newFabricManagerConfigMap("fm-agentless", "test-ns", "agentless_net")
	managerConfig.Data[networkmanager.DataKeyNATGatewayContractVersion] = "v2"
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(managerConfig).Build()
	discovery, err := networkmanager.NewDiscovery(fakeClient, "test-ns")
	if err != nil {
		t.Fatal(err)
	}
	resolver := dispatcher.NewResolver(
		dispatcheradapter.NewNetworkClassAdapter(newListingNetworkClassClient(
			[]*privatev1.NetworkClass{{Id: "nc-agentless", FabricManager: ptr.To("agentless_net")}},
			&[]*privatev1.NetworkClass{},
		)),
		discovery,
	)
	if _, _, err := resolveNATGatewayManager(
		context.Background(), resolver, "nc-agentless", "", "", "",
	); !errors.Is(err, errNATGatewayContract) {
		t.Fatalf("unsupported dispatched version error = %v, want errNATGatewayContract", err)
	}
}

func TestValidateNATGatewayProvisionResult(t *testing.T) {
	const (
		tenantID     = "tenant-a"
		gatewayID    = "11111111-1111-4111-8111-111111111111"
		externalIPID = "22222222-2222-4222-8222-222222222222"
		virtualNetID = "33333333-3333-4333-8333-333333333333"
	)

	newFixture := func(t *testing.T) (*NATGatewayReconciler, *v1alpha1.NATGateway, provisioning.NATGatewayContext, provisioning.ProvisionStatusWithExtraVars) {
		t.Helper()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := v1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		natgw := &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nat", Namespace: "networking", UID: types.UID(gatewayID), Generation: 2,
				Labels: map[string]string{osacNATGatewayIDLabel: gatewayID},
				Annotations: map[string]string{
					osacTenantKey:                           tenantID,
					osacImplementationStrategyAnnotation:    "agentless_net",
					osacNATGatewayContractVersionAnnotation: networkmanager.NATGatewayContractVersionV1,
				},
			},
			Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: virtualNetID, ExternalIP: externalIPID},
			Status: v1alpha1.NATGatewayStatus{
				DesiredConfigVersion: "desired-v1",
				ProvisioningJobs: []v1alpha1.JobStatus{{
					JobID: "ensure-job", Type: v1alpha1.JobTypeProvision,
					State: v1alpha1.JobStateSucceeded, ConfigVersion: "desired-v1",
				}},
			},
		}
		virtualNetwork := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: "network", Namespace: natgw.Namespace,
				UID:         types.UID("44444444-4444-4444-8444-444444444444"),
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetID},
				Annotations: map[string]string{osacTenantKey: tenantID},
			},
			Spec:   v1alpha1.VirtualNetworkSpec{IPv4CIDR: "10.0.0.0/16"},
			Status: v1alpha1.VirtualNetworkStatus{Phase: v1alpha1.VirtualNetworkPhaseReady},
		}
		externalIP := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "egress-ip", Namespace: natgw.Namespace,
				UID:         types.UID("55555555-5555-4555-8555-555555555555"),
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: tenantID},
			},
			Status: v1alpha1.ExternalIPStatus{
				State: v1alpha1.ExternalIPStateAllocated, Phase: v1alpha1.ExternalIPPhaseReady,
				Address: "198.51.100.10",
			},
		}
		expectedContext, pending, err := buildNATGatewayContext(natgw, virtualNetwork, externalIP)
		if err != nil || pending {
			t.Fatalf("fixture context failed: pending=%v err=%v", pending, err)
		}
		encodedContext, err := json.Marshal(expectedContext)
		if err != nil {
			t.Fatal(err)
		}
		var inputContext map[string]any
		if err := json.Unmarshal(encodedContext, &inputContext); err != nil {
			t.Fatal(err)
		}
		input := map[string]any{"osac_job_vars": map[string]any{
			"resource": map[string]any{
				"metadata": map[string]any{
					"name": natgw.Name, "namespace": natgw.Namespace, "uid": string(natgw.UID),
					"generation": natgw.Generation, "labels": natgw.Labels, "annotations": natgw.Annotations,
				},
				"spec": map[string]any{"virtualNetwork": virtualNetID, "externalIP": externalIPID},
			},
			"nat_gateway_context": inputContext,
		}}
		outputs := map[string]any{"osac_result": map[string]any{
			"schemaVersion": "v1", "operation": provisioning.NATGatewayEnsureOperation,
			"resourceUID": string(natgw.UID), "observedGeneration": natgw.Generation,
			"data": map[string]any{},
		}}
		status := provisioning.ProvisionStatusWithExtraVars{
			ProvisionStatus: provisioning.ProvisionStatus{JobID: "ensure-job", State: v1alpha1.JobStateSucceeded},
			InputExtraVars:  input, ExtraVars: outputs,
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(natgw, virtualNetwork, externalIP).
			WithStatusSubresource(natgw, virtualNetwork, externalIP).
			Build()
		return &NATGatewayReconciler{Client: k8sClient, APIReader: k8sClient}, natgw, expectedContext, status
	}

	t.Run("accepts a current same-job ensure result", func(t *testing.T) {
		reconciler, natgw, expectedContext, status := newFixture(t)
		if err := reconciler.validateNATGatewayProvisionResult(context.Background(), natgw, expectedContext, status); err != nil {
			t.Fatalf("valid NATGateway result was rejected: %v", err)
		}
	})

	t.Run("rejects stale submitted context", func(t *testing.T) {
		reconciler, natgw, expectedContext, status := newFixture(t)
		jobVars := status.InputExtraVars["osac_job_vars"].(map[string]any)
		jobVars["nat_gateway_context"].(map[string]any)["external_ip"].(map[string]any)["address"] = "198.51.100.11"
		if err := reconciler.validateNATGatewayProvisionResult(context.Background(), natgw, expectedContext, status); err == nil {
			t.Fatal("stale submitted context was accepted")
		}
	})

	t.Run("rejects a result from a different tracked job", func(t *testing.T) {
		reconciler, natgw, expectedContext, status := newFixture(t)
		status.JobID = "different-job"
		if err := reconciler.validateNATGatewayProvisionResult(context.Background(), natgw, expectedContext, status); err == nil {
			t.Fatal("result from an untracked job was accepted")
		}
	})

	t.Run("rejects changed dependency state", func(t *testing.T) {
		reconciler, natgw, expectedContext, status := newFixture(t)
		eip := &v1alpha1.ExternalIP{}
		key := client.ObjectKey{Name: "egress-ip", Namespace: natgw.Namespace}
		if err := reconciler.Get(context.Background(), key, eip); err != nil {
			t.Fatal(err)
		}
		eip.Status.Address = "198.51.100.11"
		if err := reconciler.Status().Update(context.Background(), eip); err != nil {
			t.Fatal(err)
		}
		if err := reconciler.validateNATGatewayProvisionResult(context.Background(), natgw, expectedContext, status); err == nil {
			t.Fatal("changed dependency snapshot was accepted")
		}
	})
}
