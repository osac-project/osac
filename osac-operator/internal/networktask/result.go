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

package networktask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// Result is the osac_result object of a successful job: its keys and their raw values.
type Result map[string]json.RawMessage

// OSACError is a well-formed osac_error (contract §4.6).
type OSACError struct {
	Message   string
	Retryable bool
}

// jobArtifacts holds what OSAC reads from a job's set_stats artifacts.
type jobArtifacts struct {
	result          Result
	osacError       *OSACError
	malformedError  bool
	malformedResult bool
}

// readArtifacts reads only osac_result and osac_error from AAP job artifacts.
func readArtifacts(raw json.RawMessage) jobArtifacts {
	var out jobArtifacts
	if len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	var artifacts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		return out
	}
	if value, ok := artifacts["osac_result"]; ok && !isNull(value) {
		if err := json.Unmarshal(value, &out.result); err != nil || out.result == nil {
			out.result = nil
			out.malformedResult = true
		}
	}
	if value, ok := artifacts["osac_error"]; ok && !isNull(value) {
		out.osacError, out.malformedError = parseOSACError(value)
	}
	return out
}

// parseOSACError accepts exactly {message: non-empty string, retryable: bool}.
func parseOSACError(raw json.RawMessage) (*OSACError, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 2 {
		return nil, true
	}
	var message string
	var retryable bool
	if err := strictUnmarshal(fields["message"], &message); err != nil || message == "" {
		return nil, true
	}
	if err := strictUnmarshal(fields["retryable"], &retryable); err != nil {
		return nil, true
	}
	return &OSACError{Message: message, Retryable: retryable}, false
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func strictUnmarshal(raw json.RawMessage, target any) error {
	if raw == nil || isNull(raw) {
		return errors.New("missing")
	}
	return json.Unmarshal(raw, target)
}

// RejectError is a result OSAC rejects (contract §4.6). Its message names only
// the broken rule; the engine reports "OSAC rejected the <task> result: <reason>".
type RejectError struct {
	Reason string
}

func (e *RejectError) Error() string { return e.Reason }

// Reject returns a RejectError.
func Reject(format string, args ...any) error {
	return &RejectError{Reason: fmt.Sprintf(format, args...)}
}

// RetryError fails a result for now without making the task final: the
// entry becomes Retrying with the normal backoff, and the rerun is checked
// again.
type RetryError struct{ Reason string }

func (e *RetryError) Error() string { return e.Reason }

// AcceptFunc validates the result of a successful job and applies its typed
// outputs to the object, which the engine then writes in the same status
// update that marks the entry Succeeded. It returns a RejectError to reject
// the result or another error to retry the read.
type AcceptFunc func(ctx context.Context, result Result) error

// checkKeys rejects any key outside allowed and any missing required key.
func (r Result) checkKeys(allowed []string, required ...string) error {
	allowedSet := map[string]bool{}
	for _, k := range allowed {
		allowedSet[k] = true
	}
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowedSet[k] {
			return Reject("undefined output %q", k)
		}
	}
	for _, k := range required {
		if _, ok := r[k]; !ok {
			return Reject("missing required output %q", k)
		}
	}
	return nil
}

// object decodes a JSON object and rejects keys other than keys.
func object(raw json.RawMessage, name string, keys ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, Reject("%s must be an object", name)
	}
	if len(fields) != len(keys) {
		return nil, Reject("%s must have exactly the keys %s", name, strings.Join(keys, ", "))
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			return nil, Reject("%s must have exactly the keys %s", name, strings.Join(keys, ", "))
		}
	}
	return fields, nil
}

func stringField(raw json.RawMessage, name string) (string, error) {
	var s string
	if err := strictUnmarshal(raw, &s); err != nil {
		return "", Reject("%s must be a string", name)
	}
	return s, nil
}

// canonicalIPv4 parses a canonical dotted-decimal IPv4 address.
func canonicalIPv4(value, name string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is4() || addr.String() != value {
		return netip.Addr{}, Reject("%s %q is not a canonical IPv4 address", name, value)
	}
	return addr, nil
}

// AcceptNoOutput rejects any osac_result key: the task has no typed output.
func AcceptNoOutput(_ context.Context, result Result) error {
	return result.checkKeys(nil)
}

// AcceptSubnetEligibility records the optional Kubernetes create_subnet eligibility
// result. Older managers with no output support VM attachments by default; the
// attachment task remains authoritative before any VM resources are created.
func AcceptSubnetEligibility(subnet *v1alpha1.Subnet, result Result) error {
	if err := result.checkKeys([]string{"compute_instance_supported", "compute_instance_unsupported_reason"}); err != nil {
		return err
	}
	supported := true
	if raw, ok := result["compute_instance_supported"]; ok {
		if err := strictUnmarshal(raw, &supported); err != nil {
			return Reject("compute_instance_supported must be a boolean")
		}
	}
	reason := ""
	if raw, ok := result["compute_instance_unsupported_reason"]; ok {
		var err error
		reason, err = stringField(raw, "compute_instance_unsupported_reason")
		if err != nil {
			return err
		}
	}
	if supported && reason != "" {
		return Reject("supported Subnet cannot have an unsupported reason")
	}
	subnet.Status.ComputeInstanceSupported = &supported
	subnet.Status.ComputeInstanceUnsupportedReason = reason
	return nil
}

// ParseBackendID reads the optional backend_id of create_fabric_domain.
func ParseBackendID(result Result) (string, error) {
	if err := result.checkKeys([]string{"backend_id"}); err != nil {
		return "", err
	}
	return backendID(result)
}

// backendID reads the optional backend_id key.
func backendID(result Result) (string, error) {
	raw, ok := result["backend_id"]
	if !ok {
		return "", nil
	}
	return stringField(raw, "backend_id")
}

// ParseVirtualNetworkResult reads the result of a Fabric create_virtual_network
// (contract §4.9, EP #427): with a network type the integer l3_vni is required
// and backend_id is optional; without one only backend_id is defined.
// L3 VNI allocation and uniqueness are owned by the Fabric Manager.
func ParseVirtualNetworkResult(result Result, networkType string) (string, int32, error) {
	if networkType == "" {
		if _, ok := result["l3_vni"]; ok {
			return "", 0, Reject("l3_vni returned without a network type")
		}
		id, err := ParseBackendID(result)
		return id, 0, err
	}
	if err := result.checkKeys([]string{"backend_id", "l3_vni"}, "l3_vni"); err != nil {
		return "", 0, err
	}
	l3, ok := vni(result["l3_vni"])
	if !ok {
		return "", 0, Reject("l3_vni must be an integer from 1 to 16777215")
	}
	id, err := backendID(result)
	if err != nil {
		return "", 0, err
	}
	return id, l3, nil
}

// ParseNetworkSegment reads the network_segment of a Fabric create_subnet.
// networkType is the network_type of the input; without it the key is
// undefined. It checks the shape rules of contract §4.9: {type, l2_vni}; the
// L3 VNI belongs to the VirtualNetwork (ParseVirtualNetworkResult). VNI
// allocation and uniqueness are owned by the Fabric Manager.
func ParseNetworkSegment(result Result, networkType string) (*v1alpha1.NetworkSegment, error) {
	if networkType == "" {
		if _, ok := result["network_segment"]; ok {
			return nil, Reject("network segment returned without a network type")
		}
		return nil, result.checkKeys(nil)
	}
	if err := result.checkKeys([]string{"network_segment"}, "network_segment"); err != nil {
		return nil, err
	}
	fields, err := object(result["network_segment"], "network_segment", "type", "l2_vni")
	if err != nil {
		return nil, err
	}
	segType, err := stringField(fields["type"], "network_segment.type")
	if err != nil {
		return nil, err
	}
	if segType != networkType {
		return nil, Reject("network segment type does not match the network type")
	}
	l2, ok := vni(fields["l2_vni"])
	if !ok {
		return nil, Reject("network segment l2_vni must be an integer from 1 to 16777215")
	}
	return &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkType(segType), L2VNI: l2}, nil
}

// vni accepts only a JSON integer from 1 to 16777215 (a string is rejected).
func vni(raw json.RawMessage) (int32, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, false
	}
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(n.String(), ".eE") {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil || i < 1 || i > 16777215 {
		return 0, false
	}
	return int32(i), true
}

// ParseDHCPLease reads the dhcp_lease of query_dhcp_lease: the MAC equals the
// input MAC, and the IP is canonical, in physical_ipv4_cidrs, and not the
// Subnet's network, broadcast or gateway address.
func ParseDHCPLease(result Result, inputMAC, subnetCIDR string, ipam *v1alpha1.SubnetIPAM) (*v1alpha1.NetworkAttachmentDHCPLease, error) {
	if err := result.checkKeys([]string{"dhcp_lease"}, "dhcp_lease"); err != nil {
		return nil, err
	}
	fields, err := object(result["dhcp_lease"], "dhcp_lease", "ip_address", "mac_address")
	if err != nil {
		return nil, err
	}
	ip, err := stringField(fields["ip_address"], "dhcp_lease.ip_address")
	if err != nil {
		return nil, err
	}
	mac, err := stringField(fields["mac_address"], "dhcp_lease.mac_address")
	if err != nil {
		return nil, err
	}
	if mac != inputMAC {
		return nil, Reject("dhcp_lease.mac_address does not equal the interface MAC address")
	}
	addr, err := canonicalIPv4(ip, "dhcp_lease.ip_address")
	if err != nil {
		return nil, err
	}
	if ipam == nil || !inCIDRs(addr, ipam.PhysicalIPv4CIDRs) {
		return nil, Reject("dhcp_lease.ip_address is not in physical_ipv4_cidrs")
	}
	network, broadcast, err := networkAndBroadcast(subnetCIDR)
	if err != nil {
		return nil, err
	}
	if addr == network || addr == broadcast || addr.String() == ipam.GatewayIPv4 {
		return nil, Reject("dhcp_lease.ip_address is the Subnet's network, broadcast or gateway address")
	}
	return &v1alpha1.NetworkAttachmentDHCPLease{IPAddress: ip, MACAddress: mac}, nil
}

// ParseExternalIP reads the external_ip of create_external_ip: a canonical
// eligible address of the pool CIDR (all but the network and broadcast
// addresses; every address of a /31 or /32).
func ParseExternalIP(result Result, poolCIDR string) (string, error) {
	if err := result.checkKeys([]string{"external_ip"}, "external_ip"); err != nil {
		return "", err
	}
	fields, err := object(result["external_ip"], "external_ip", "address")
	if err != nil {
		return "", err
	}
	address, err := stringField(fields["address"], "external_ip.address")
	if err != nil {
		return "", err
	}
	addr, err := canonicalIPv4(address, "external_ip.address")
	if err != nil {
		return "", err
	}
	pool, err := netip.ParsePrefix(poolCIDR)
	if err != nil {
		return "", fmt.Errorf("pool CIDR %q: %w", poolCIDR, err)
	}
	if !pool.Contains(addr) {
		return "", Reject("external_ip.address is not in the pool")
	}
	if pool.Bits() < 31 {
		network, broadcast, _ := networkAndBroadcast(poolCIDR)
		if addr == network || addr == broadcast {
			return "", Reject("external_ip.address is the pool's network or broadcast address")
		}
	}
	return address, nil
}

// ParseClusterIngress reads the cluster_ingress of reserve_cluster_ingress_ip:
// a canonical address in lb_vip_ipv4_cidrs, not the Subnet broadcast address,
// equal to accepted when an address was already accepted.
func ParseClusterIngress(result Result, subnetCIDR string, ipam *v1alpha1.SubnetIPAM, accepted string) (string, error) {
	if err := result.checkKeys([]string{"cluster_ingress"}, "cluster_ingress"); err != nil {
		return "", err
	}
	fields, err := object(result["cluster_ingress"], "cluster_ingress", "address")
	if err != nil {
		return "", err
	}
	address, err := stringField(fields["address"], "cluster_ingress.address")
	if err != nil {
		return "", err
	}
	addr, err := canonicalIPv4(address, "cluster_ingress.address")
	if err != nil {
		return "", err
	}
	if ipam == nil || !inCIDRs(addr, ipam.LBVIPIPv4CIDRs) {
		return "", Reject("cluster_ingress.address is not in lb_vip_ipv4_cidrs")
	}
	if _, broadcast, err := networkAndBroadcast(subnetCIDR); err == nil && addr == broadcast {
		return "", Reject("cluster_ingress.address is the Subnet's broadcast address")
	}
	if accepted != "" && accepted != address {
		return "", Reject("cluster_ingress.address differs from the address already accepted for the cluster")
	}
	return address, nil
}

// ParseVMAttachment reads the vm_attachment of the Kubernetes
// apply_network_attachment: only namespace, and the namespace exists (read from
// the API server, not a cache) with osac.openshift.io/tenant equal to tenant.
func ParseVMAttachment(ctx context.Context, apiReader client.Reader, result Result, tenant string) (*v1alpha1.NetworkAttachmentVMAttachment, error) {
	if tenant == "" {
		return nil, Reject("the NetworkAttachment has no tenant")
	}
	if err := result.checkKeys([]string{"vm_attachment"}, "vm_attachment"); err != nil {
		return nil, err
	}
	fields, err := object(result["vm_attachment"], "vm_attachment", "namespace")
	if err != nil {
		return nil, err
	}
	namespace, err := stringField(fields["namespace"], "vm_attachment.namespace")
	if err != nil || namespace == "" {
		return nil, Reject("vm_attachment.namespace must be a non-empty string")
	}
	ns := &corev1.Namespace{}
	if err := apiReader.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, Reject("vm_attachment namespace %s does not exist", namespace)
		}
		return nil, err
	}
	if value, ok := ns.Labels[TenantAnnotation]; !ok || value != tenant {
		return nil, Reject("vm_attachment namespace %s is not labelled %s with the tenant", namespace, TenantAnnotation)
	}
	return &v1alpha1.NetworkAttachmentVMAttachment{Namespace: namespace}, nil
}
