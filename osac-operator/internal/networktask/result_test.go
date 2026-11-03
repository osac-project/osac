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
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func result(t *testing.T, raw string) Result {
	t.Helper()
	var r Result
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func expectReject(t *testing.T, err error, reason string) {
	t.Helper()
	var reject *RejectError
	if !errors.As(err, &reject) || reject.Reason != reason {
		t.Fatalf("got %v, want rejection %q", err, reason)
	}
}

func TestLayout(t *testing.T) {
	cases := []struct {
		plan                     v1alpha1.SubnetAddressPlan
		overlay, physical, lbVip []string
	}{
		{v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4}, []string{"10.0.1.0/25"}, []string{"10.0.1.128/26"}, []string{"10.0.1.192/26"}},
		{v1alpha1.SubnetAddressPlan{Overlay: 12, Fabric: 4}, []string{"10.0.1.0/25", "10.0.1.128/26"}, []string{"10.0.1.192/26"}, []string{}},
		{v1alpha1.SubnetAddressPlan{Overlay: 16}, []string{"10.0.1.0/24"}, []string{}, []string{}},
		{v1alpha1.SubnetAddressPlan{Overlay: 9, Fabric: 4, LBVIP: 3},
			[]string{"10.0.1.0/25", "10.0.1.128/28"},
			[]string{"10.0.1.144/28", "10.0.1.160/27", "10.0.1.192/28"},
			[]string{"10.0.1.208/28", "10.0.1.224/27"}},
		{v1alpha1.SubnetAddressPlan{Fabric: 12, LBVIP: 4}, []string{}, []string{"10.0.1.0/25", "10.0.1.128/26"}, []string{"10.0.1.192/26"}},
		{v1alpha1.SubnetAddressPlan{Fabric: 16}, []string{}, []string{"10.0.1.0/24"}, []string{}},
	}
	for _, tc := range cases {
		ipam, err := Layout("10.0.1.0/24", tc.plan)
		if err != nil {
			t.Fatal(err)
		}
		if ipam.GatewayIPv4 != "10.0.1.1" || !reflect.DeepEqual(ipam.OverlayIPv4CIDRs, tc.overlay) ||
			!reflect.DeepEqual(ipam.PhysicalIPv4CIDRs, tc.physical) || !reflect.DeepEqual(ipam.LBVIPIPv4CIDRs, tc.lbVip) {
			t.Fatalf("plan %+v: got %+v", tc.plan, ipam)
		}
	}
	ipam, err := Layout("10.0.1.0/26", v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4})
	if err != nil || !reflect.DeepEqual(ipam.LBVIPIPv4CIDRs, []string{"10.0.1.48/28"}) {
		t.Fatalf("/26: %+v %v", ipam, err)
	}
	for _, bad := range []struct {
		cidr string
		plan v1alpha1.SubnetAddressPlan
	}{
		{"10.0.1.0/24", v1alpha1.SubnetAddressPlan{Overlay: 8}},
		{"10.0.1.1/24", v1alpha1.SubnetAddressPlan{Overlay: 16}},
		{"10.0.1.0/29", v1alpha1.SubnetAddressPlan{Overlay: 16}},
	} {
		if _, err := Layout(bad.cidr, bad.plan); err == nil {
			t.Fatalf("%s %+v accepted", bad.cidr, bad.plan)
		}
	}
	vars := IPAMVars(&v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1", OverlayIPv4CIDRs: []string{"10.0.1.0/24"}})
	data, _ := json.Marshal(vars)
	if string(data) != `{"gateway_ipv4":"10.0.1.1","lb_vip_ipv4_cidrs":[],"overlay_ipv4_cidrs":["10.0.1.0/24"],"physical_ipv4_cidrs":[]}` {
		t.Fatalf("ipam vars %s", data)
	}
}

func TestReadArtifacts(t *testing.T) {
	a := readArtifacts(json.RawMessage(`{"osac_result":{"backend_id":"x"},"osac_error":{"message":"m","retryable":true},"other":1}`))
	if a.osacError == nil || !a.osacError.Retryable || a.result["backend_id"] == nil {
		t.Fatalf("%+v", a)
	}
	for _, bad := range []string{
		`{"osac_error":{"message":"","retryable":true}}`,
		`{"osac_error":{"message":"m"}}`,
		`{"osac_error":{"message":1,"retryable":true}}`,
		`{"osac_error":"boom"}`,
	} {
		if a := readArtifacts(json.RawMessage(bad)); a.osacError != nil || !a.malformedError {
			t.Fatalf("%s accepted", bad)
		}
	}
	if a := readArtifacts(json.RawMessage(`{"osac_result":[1]}`)); !a.malformedResult {
		t.Fatal("list osac_result accepted")
	}
}

func TestParseBackendID(t *testing.T) {
	if id, err := ParseBackendID(result(t, `{"backend_id":"vpc-1"}`)); err != nil || id != "vpc-1" {
		t.Fatal(id, err)
	}
	if id, err := ParseBackendID(result(t, `{}`)); err != nil || id != "" {
		t.Fatal(id, err)
	}
	_, err := ParseBackendID(result(t, `{"backend_id":1}`))
	expectReject(t, err, "backend_id must be a string")
	_, err = ParseBackendID(result(t, `{"vpc_id":"1"}`))
	expectReject(t, err, `undefined output "vpc_id"`)
}

func TestParseVirtualNetworkResult(t *testing.T) {
	ok := map[string]struct {
		raw, networkType, id string
		l3                   int32
	}{
		"l3_vni only":       {`{"l3_vni":20001}`, "evpn", "", 20001},
		"backend_id and l3": {`{"backend_id":"x","l3_vni":20001}`, "evpn", "x", 20001},
		"minimum":           {`{"l3_vni":1}`, "evpn", "", 1},
		"maximum":           {`{"l3_vni":16777215}`, "evpn", "", 16777215},
		"no type":           {`{"backend_id":"x"}`, "", "x", 0},
		"no type empty":     {`{}`, "", "", 0},
	}
	for name, tc := range ok {
		id, l3, err := ParseVirtualNetworkResult(result(t, tc.raw), tc.networkType)
		if err != nil || id != tc.id || l3 != tc.l3 {
			t.Fatalf("%s: got (%q, %d, %v)", name, id, l3, err)
		}
	}
	const vniRule = "l3_vni must be an integer from 1 to 16777215"
	bad := map[string]struct {
		raw, networkType, reason string
	}{
		"missing":           {`{}`, "evpn", `missing required output "l3_vni"`},
		"string":            {`{"l3_vni":"20001"}`, "evpn", vniRule},
		"float":             {`{"l3_vni":20001.5}`, "evpn", vniRule},
		"zero":              {`{"l3_vni":0}`, "evpn", vniRule},
		"too large":         {`{"l3_vni":16777216}`, "evpn", vniRule},
		"extra key":         {`{"l3_vni":20001,"extra":1}`, "evpn", `undefined output "extra"`},
		"backend_id number": {`{"backend_id":1,"l3_vni":20001}`, "evpn", "backend_id must be a string"},
		"no type with l3":   {`{"l3_vni":1}`, "", "l3_vni returned without a network type"},
		"no type extra":     {`{"vpc_id":"1"}`, "", `undefined output "vpc_id"`},
	}
	for name, tc := range bad {
		_, _, err := ParseVirtualNetworkResult(result(t, tc.raw), tc.networkType)
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		expectReject(t, err, tc.reason)
	}
}

func TestParseNetworkSegment(t *testing.T) {
	seg, err := ParseNetworkSegment(result(t, `{"network_segment":{"type":"evpn","l2_vni":10100}}`), "evpn")
	if err != nil || *seg != (v1alpha1.NetworkSegment{Type: "evpn", L2VNI: 10100}) {
		t.Fatal(seg, err)
	}
	if seg, err := ParseNetworkSegment(result(t, `{}`), ""); err != nil || seg != nil {
		t.Fatal(seg, err)
	}
	const keys = "network_segment must have exactly the keys type, l2_vni"
	const vniRule = "network segment l2_vni must be an integer from 1 to 16777215"
	cases := map[string]struct {
		raw, networkType, reason string
	}{
		"without network type": {`{"network_segment":{"type":"evpn","l2_vni":1}}`, "", "network segment returned without a network type"},
		"missing":              {`{}`, "evpn", `missing required output "network_segment"`},
		"string VNI":           {`{"network_segment":{"type":"evpn","l2_vni":"10100"}}`, "evpn", vniRule},
		"float VNI":            {`{"network_segment":{"type":"evpn","l2_vni":10100.5}}`, "evpn", vniRule},
		"zero VNI":             {`{"network_segment":{"type":"evpn","l2_vni":0}}`, "evpn", vniRule},
		"too large VNI":        {`{"network_segment":{"type":"evpn","l2_vni":16777216}}`, "evpn", vniRule},
		"missing l2_vni":       {`{"network_segment":{"type":"evpn"}}`, "evpn", keys},
		"l3_vni in segment":    {`{"network_segment":{"type":"evpn","l2_vni":10100,"l3_vni":20001}}`, "evpn", keys},
		"wrong type":           {`{"network_segment":{"type":"vlan","l2_vni":1}}`, "evpn", "network segment type does not match the network type"},
		"extra key":            {`{"network_segment":{"type":"evpn","l2_vni":1,"rt":"x"}}`, "evpn", keys},
		"extra top-level key":  {`{"network_segment":{"type":"evpn","l2_vni":1},"x":1}`, "evpn", `undefined output "x"`},
	}
	for name, tc := range cases {
		_, err := ParseNetworkSegment(result(t, tc.raw), tc.networkType)
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		expectReject(t, err, tc.reason)
	}
}

func TestParseDHCPLease(t *testing.T) {
	ipam := &v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1", PhysicalIPv4CIDRs: []string{"10.0.1.128/26"}}
	mac := "aa:bb:cc:dd:ee:ff"
	lease, err := ParseDHCPLease(result(t, `{"dhcp_lease":{"ip_address":"10.0.1.130","mac_address":"aa:bb:cc:dd:ee:ff"}}`), mac, "10.0.1.0/24", ipam)
	if err != nil || lease.IPAddress != "10.0.1.130" {
		t.Fatal(lease, err)
	}
	cases := map[string]string{
		`{"dhcp_lease":{"ip_address":"10.0.1.130","mac_address":"aa:bb:cc:dd:ee:00"}}`:  "dhcp_lease.mac_address does not equal the interface MAC address",
		`{"dhcp_lease":{"ip_address":"10.0.1.10","mac_address":"aa:bb:cc:dd:ee:ff"}}`:   "dhcp_lease.ip_address is not in physical_ipv4_cidrs",
		`{"dhcp_lease":{"ip_address":"10.0.1.0130","mac_address":"aa:bb:cc:dd:ee:ff"}}`: `dhcp_lease.ip_address "10.0.1.0130" is not a canonical IPv4 address`,
		`{"dhcp_lease":{"ip_address":"10.0.1.130"}}`:                                    "dhcp_lease must have exactly the keys ip_address, mac_address",
		`{}`: `missing required output "dhcp_lease"`,
	}
	for raw, reason := range cases {
		_, err := ParseDHCPLease(result(t, raw), mac, "10.0.1.0/24", ipam)
		expectReject(t, err, reason)
	}
	// 0/1/0 plan: the gateway, network and broadcast addresses are in the physical range.
	all := &v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1", PhysicalIPv4CIDRs: []string{"10.0.1.0/24"}}
	for _, ip := range []string{"10.0.1.1", "10.0.1.0", "10.0.1.255"} {
		_, err := ParseDHCPLease(result(t, `{"dhcp_lease":{"ip_address":"`+ip+`","mac_address":"aa:bb:cc:dd:ee:ff"}}`), mac, "10.0.1.0/24", all)
		expectReject(t, err, "dhcp_lease.ip_address is the Subnet's network, broadcast or gateway address")
	}
}

func TestParseExternalIP(t *testing.T) {
	if a, err := ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.19"}}`), "198.51.100.16/29"); err != nil || a != "198.51.100.19" {
		t.Fatal(a, err)
	}
	for _, pool := range []string{"198.51.100.18/31", "198.51.100.19/32"} {
		if _, err := ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.19"}}`), pool); err != nil {
			t.Fatalf("%s: %v", pool, err)
		}
	}
	_, err := ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.16"}}`), "198.51.100.16/29")
	expectReject(t, err, "external_ip.address is the pool's network or broadcast address")
	_, err = ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.23"}}`), "198.51.100.16/29")
	expectReject(t, err, "external_ip.address is the pool's network or broadcast address")
	_, err = ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.40"}}`), "198.51.100.16/29")
	expectReject(t, err, "external_ip.address is not in the pool")
	_, err = ParseExternalIP(result(t, `{"external_ip":{"address":"198.51.100.19/32"}}`), "198.51.100.16/29")
	expectReject(t, err, `external_ip.address "198.51.100.19/32" is not a canonical IPv4 address`)
}

func TestParseClusterIngress(t *testing.T) {
	ipam := &v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1", LBVIPIPv4CIDRs: []string{"10.0.1.192/26"}}
	if a, err := ParseClusterIngress(result(t, `{"cluster_ingress":{"address":"10.0.1.193"}}`), "10.0.1.0/24", ipam, ""); err != nil || a != "10.0.1.193" {
		t.Fatal(a, err)
	}
	_, err := ParseClusterIngress(result(t, `{"cluster_ingress":{"address":"10.0.1.255"}}`), "10.0.1.0/24", ipam, "")
	expectReject(t, err, "cluster_ingress.address is the Subnet's broadcast address")
	_, err = ParseClusterIngress(result(t, `{"cluster_ingress":{"address":"10.0.1.10"}}`), "10.0.1.0/24", ipam, "")
	expectReject(t, err, "cluster_ingress.address is not in lb_vip_ipv4_cidrs")
	_, err = ParseClusterIngress(result(t, `{"cluster_ingress":{"address":"10.0.1.194"}}`), "10.0.1.0/24", ipam, "10.0.1.193")
	expectReject(t, err, "cluster_ingress.address differs from the address already accepted for the cluster")
}

func TestParseVMAttachment(t *testing.T) {
	scheme := testScheme(t)
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "osac-net-1", Labels: map[string]string{TenantAnnotation: "tenant-a"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled"}},
	).Build()
	var reader client.Reader = c
	ctx := context.Background()
	if a, err := ParseVMAttachment(ctx, reader, result(t, `{"vm_attachment":{"namespace":"osac-net-1"}}`), "tenant-a"); err != nil || a.Namespace != "osac-net-1" {
		t.Fatal(a, err)
	}
	cases := map[string]string{
		`{"vm_attachment":{}}`:                                       "vm_attachment must have exactly the keys namespace",
		`{"vm_attachment":{"namespace":"missing"}}`:                  "vm_attachment namespace missing does not exist",
		`{"vm_attachment":{"namespace":"unlabelled"}}`:               "vm_attachment namespace unlabelled is not labelled osac.openshift.io/tenant with the tenant",
		`{"vm_attachment":{"namespace":"osac-net-1","network":"x"}}`: "vm_attachment must have exactly the keys namespace",
		`{"vm_attachment":{"namespace":"osac-net-1"},"labels":{}}`:   `undefined output "labels"`,
	}
	for raw, reason := range cases {
		_, err := ParseVMAttachment(ctx, reader, result(t, raw), "tenant-a")
		expectReject(t, err, reason)
	}
	_, err := ParseVMAttachment(ctx, reader, result(t, `{"vm_attachment":{"namespace":"osac-net-1"}}`), "tenant-b")
	expectReject(t, err, "vm_attachment namespace osac-net-1 is not labelled osac.openshift.io/tenant with the tenant")
	_, err = ParseVMAttachment(ctx, reader, result(t, `{"vm_attachment":{"namespace":"unlabelled"}}`), "")
	expectReject(t, err, "the NetworkAttachment has no tenant")
}

func TestSnapshotAndInput(t *testing.T) {
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Name: "co", Namespace: "ns", UID: "uid-co", Generation: 3,
		Labels:      map[string]string{"osac.openshift.io/clusterorder-uuid": "c1"},
		Annotations: map[string]string{TenantAnnotation: "tenant-a"},
	}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: "t", PullSecret: "secret"}}
	snap, err := Snapshot(co, "ClusterOrder", co.Spec)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(snap)
	want := `{"apiVersion":"osac.openshift.io/v1alpha1","id":"c1","kind":"ClusterOrder","metadata":{"generation":3,"name":"co","namespace":"ns","uid":"uid-co"},"spec":{}}`
	if string(data) != want {
		t.Fatalf("got %s", data)
	}
	sg := &v1alpha1.SecurityGroup{ObjectMeta: metav1.ObjectMeta{Name: "sg", Labels: map[string]string{"osac.openshift.io/securitygroup-uuid": "g1"}},
		Spec: v1alpha1.SecurityGroupSpec{VirtualNetwork: "vn1", IngressRules: []v1alpha1.SecurityRule{{Protocol: "all", SourceCIDR: "10.0.0.0/8"}}}}
	snap, _ = Snapshot(sg, "SecurityGroup", sg.Spec, "ingressRules", "egressRules")
	spec := snap["spec"].(map[string]any)
	if _, ok := spec["virtualNetwork"]; ok || spec["ingressRules"] == nil {
		t.Fatalf("spec %v", spec)
	}
	input := NewInput(co, "ClusterOrder", snap)
	if input["tenant"] != "tenant-a" {
		t.Fatal("tenant missing")
	}
	pool := &v1alpha1.ExternalIPPool{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{TenantAnnotation: "x"}}}
	if _, ok := NewInput(pool, "ExternalIPPool", nil)["tenant"]; ok {
		t.Fatal("pool input has tenant")
	}
	na := &v1alpha1.NetworkAttachment{Spec: v1alpha1.NetworkAttachmentSpec{Target: v1alpha1.NetworkAttachmentTarget{Kind: "ComputeInstance", ID: "ci-1"}}}
	if APIID(na, "NetworkAttachment") != "ci-1" {
		t.Fatal("NetworkAttachment id is not the target id")
	}
	stored := &v1alpha1.Subnet{}
	if err := StoreInput(stored, input); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInput(stored)
	if err != nil || loaded["tenant"] != "tenant-a" {
		t.Fatal(loaded, err)
	}
	vars := WithLaunchKeys(loaded, map[string]any{"network_type": "evpn"})
	if vars["network_type"] != "evpn" || loaded["network_type"] != nil {
		t.Fatal("launch keys leaked into the stored input")
	}
}
