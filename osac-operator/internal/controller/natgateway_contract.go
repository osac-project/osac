/*
Copyright 2026.

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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

const (
	// inputNATGatewayContext is the stored input key of the dependency snapshot.
	inputNATGatewayContext = "nat_gateway_context"
	// The role records a dependency's readiness with these values. The networking
	// CRs have a Ready condition and no phase or state, so a snapshot that is
	// built at all carries exactly these.
	natGatewayContextPhaseReady     = "Ready"
	natGatewayContextStateAllocated = "Allocated"
)

// natGatewayContext is the controller-resolved dependency snapshot of a
// NATGateway task: managers that keep provider state by Kubernetes UID use it
// without querying Kubernetes (osac.templates.agentless_net does).
type natGatewayContext struct {
	ExternalIP     natGatewayExternalIPContext     `json:"external_ip"`
	VirtualNetwork natGatewayVirtualNetworkContext `json:"virtual_network"`
}

type natGatewayExternalIPContext struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	FulfillmentID string `json:"fulfillment_id"`
	KubernetesUID string `json:"kubernetes_uid"`
	TenantID      string `json:"tenant_id"`
	Address       string `json:"address"`
	State         string `json:"state"`
	Phase         string `json:"phase"`
}

type natGatewayVirtualNetworkContext struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	FulfillmentID string `json:"fulfillment_id"`
	KubernetesUID string `json:"kubernetes_uid"`
	TenantID      string `json:"tenant_id"`
	IPv4CIDR      string `json:"ipv4_cidr"`
	Phase         string `json:"phase"`
}

// buildNATGatewayContext validates the dependencies of a NATGateway and returns
// their snapshot. pending is true while a dependency is not ready yet or any of
// the three is being deleted. An error is an identity, tenant or address
// violation that waiting does not fix.
func buildNATGatewayContext(
	natgw *v1alpha1.NATGateway,
	vn *v1alpha1.VirtualNetwork,
	eip *v1alpha1.ExternalIP,
) (natGatewayContext, bool, error) {
	if natgw == nil || vn == nil || eip == nil {
		return natGatewayContext{}, false, errors.New("NATGateway dependencies are required")
	}
	if natgw.UID == "" || natgw.Labels[osacNATGatewayIDLabel] == "" || vn.UID == "" || eip.UID == "" {
		return natGatewayContext{}, false, errors.New("NATGateway identity and dependency UIDs are required")
	}
	tenantID := natgw.Annotations[networktask.TenantAnnotation]
	if tenantID == "" || vn.Annotations[networktask.TenantAnnotation] != tenantID ||
		eip.Annotations[networktask.TenantAnnotation] != tenantID {
		return natGatewayContext{}, false, errors.New("NATGateway dependency tenant ownership does not match")
	}
	if natgw.Namespace != vn.Namespace || natgw.Namespace != eip.Namespace {
		return natGatewayContext{}, false, errors.New("NATGateway dependencies must share the networking namespace")
	}
	if vn.Labels[osacVirtualNetworkIDLabel] != natgw.Spec.VirtualNetwork ||
		eip.Labels[osacExternalIPIDLabel] != natgw.Spec.ExternalIP {
		return natGatewayContext{}, false, errors.New("NATGateway dependency identity does not match its references")
	}
	if !natgw.DeletionTimestamp.IsZero() || !isReady(vn) || !allocated(eip) {
		return natGatewayContext{}, true, nil
	}
	if address, err := netip.ParseAddr(eip.Status.Address); err != nil || !address.Is4() ||
		address.IsUnspecified() || address.String() != eip.Status.Address {
		return natGatewayContext{}, false, errors.New("NATGateway ExternalIP address must be canonical IPv4")
	}
	if prefix, err := netip.ParsePrefix(vn.Spec.IPv4CIDR); err != nil || !prefix.Addr().Is4() ||
		prefix != prefix.Masked() || prefix.String() != vn.Spec.IPv4CIDR {
		return natGatewayContext{}, false, errors.New("NATGateway VirtualNetwork IPv4 CIDR must be canonical IPv4")
	}
	return natGatewayContext{
		ExternalIP: natGatewayExternalIPContext{
			Name: eip.Name, Namespace: eip.Namespace,
			FulfillmentID: eip.Labels[osacExternalIPIDLabel],
			KubernetesUID: string(eip.UID), TenantID: tenantID,
			Address: eip.Status.Address, State: natGatewayContextStateAllocated,
			Phase: natGatewayContextPhaseReady,
		},
		VirtualNetwork: natGatewayVirtualNetworkContext{
			Name: vn.Name, Namespace: vn.Namespace,
			FulfillmentID: vn.Labels[osacVirtualNetworkIDLabel],
			KubernetesUID: string(vn.UID), TenantID: tenantID,
			IPv4CIDR: vn.Spec.IPv4CIDR, Phase: natGatewayContextPhaseReady,
		},
	}, false, nil
}

// natGatewayContextVars is the snake_case form of the snapshot in a task input.
func natGatewayContextVars(value natGatewayContext) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encoding the NATGateway context: %w", err)
	}
	vars := map[string]any{}
	if err := json.Unmarshal(encoded, &vars); err != nil {
		return nil, fmt.Errorf("encoding the NATGateway context: %w", err)
	}
	return vars, nil
}

// natGatewaySubmitted is the part of the stored input the result is checked against.
type natGatewaySubmitted struct {
	Resource struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
	} `json:"resource"`
	Context natGatewayContext `json:"nat_gateway_context"`
}

func decodeNATGatewaySubmitted(input map[string]any) (natGatewaySubmitted, error) {
	var submitted natGatewaySubmitted
	encoded, err := json.Marshal(input)
	if err != nil {
		return submitted, err
	}
	err = json.Unmarshal(encoded, &submitted)
	return submitted, err
}

// acceptDependencies accepts a create_nat_gateway result only while the
// NATGateway and its dependencies still match the input the job received. It
// reads through the API server, not the cache. A rejection leaves the provider
// state to the delete task.
func (r *NATGatewayReconciler) acceptDependencies(
	ctx context.Context, natgw *v1alpha1.NATGateway, input map[string]any,
) error {
	submitted, err := decodeNATGatewaySubmitted(input)
	if err != nil {
		return networktask.Reject("the stored NATGateway input cannot be read")
	}
	if submitted.Resource.Metadata.Generation != natgw.Generation {
		return networktask.Reject("NATGateway changed while the provider operation was running")
	}
	reader := r.Engine.APIReader()
	vn, err := findVirtualNetwork(ctx, reader, natgw.Namespace, natgw.Spec.VirtualNetwork)
	if err != nil {
		return err
	}
	eip, err := findExternalIP(ctx, reader, natgw.Namespace, natgw.Spec.ExternalIP)
	if err != nil {
		return err
	}
	if vn == nil || eip == nil {
		return networktask.Reject("NATGateway dependencies are no longer Ready")
	}
	current, pending, err := buildNATGatewayContext(natgw, vn, eip)
	if err != nil {
		return networktask.Reject("%s", err.Error())
	}
	if pending {
		return networktask.Reject("NATGateway dependencies are no longer Ready")
	}
	if current != submitted.Context {
		return networktask.Reject("NATGateway dependencies changed while the provider operation was running")
	}
	return nil
}
