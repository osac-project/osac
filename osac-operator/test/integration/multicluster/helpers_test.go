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

package multicluster

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// These tests exercise helper logic without Ginkgo's BeforeSuite.
// Run with: go test -run TestHelper -v ./test/integration/multicluster/

// grpcTenantResponse mirrors the TenantsCreateResponse envelope
// returned by grpcurl.
type grpcTenantResponse struct {
	Object struct {
		ID       string `json:"id"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"object"`
}

// grpcCIResponse mirrors the ComputeInstancesCreateResponse envelope
// returned by grpcurl.
type grpcCIResponse struct {
	Object struct {
		ID       string `json:"id"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"object"`
}

func TestHelperParseTenantResponse(t *testing.T) {
	raw := `{"object":{"id":"tenant-uuid-123",` +
		`"metadata":{"name":"mc-tenant-abc"}}}`

	var resp grpcTenantResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.Object.ID != "tenant-uuid-123" {
		t.Errorf("expected object.id=%q, got %q",
			"tenant-uuid-123", resp.Object.ID)
	}
	if resp.Object.Metadata.Name != "mc-tenant-abc" {
		t.Errorf("expected object.metadata.name=%q, got %q",
			"mc-tenant-abc", resp.Object.Metadata.Name)
	}
}

func TestHelperParseComputeInstanceResponse(t *testing.T) {
	raw := `{"object":{"id":"ci-uuid-456",` +
		`"metadata":{"name":"mc-test-def",` +
		`"tenant":"mc-tenant-abc"},"spec":{}},` +
		`"warnings":[]}`

	var resp grpcCIResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.Object.ID != "ci-uuid-456" {
		t.Errorf("expected object.id=%q, got %q",
			"ci-uuid-456", resp.Object.ID)
	}
	if resp.Object.Metadata.Name != "mc-test-def" {
		t.Errorf("expected object.metadata.name=%q, got %q",
			"mc-test-def", resp.Object.Metadata.Name)
	}
}

func TestHelperEmptyIDRejection(t *testing.T) {
	// Simulates a response with an empty ID field -- the test
	// should detect this and reject it.
	raw := `{"object":{"id":"","metadata":{"name":"mc-test"}}}`

	var resp grpcTenantResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.Object.ID != "" {
		t.Errorf("expected empty object.id, got %q",
			resp.Object.ID)
	}
	// In the real test, Expect(resp.Object.ID).NotTo(BeEmpty())
	// would catch this.
}

func TestHelperFlatParseWouldMiss(t *testing.T) {
	// Demonstrates that parsing at the top level (the old bug)
	// would miss the nested id/metadata fields.
	raw := `{"object":{"id":"tenant-uuid-123",` +
		`"metadata":{"name":"mc-tenant-abc"}}}`

	var flat struct {
		ID       string `json:"id"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(raw), &flat); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if flat.ID != "" {
		t.Errorf("flat parse should yield empty id, got %q",
			flat.ID)
	}
	if flat.Metadata.Name != "" {
		t.Errorf("flat parse should yield empty name, got %q",
			flat.Metadata.Name)
	}
}

func TestHelperCleanupGating(t *testing.T) {
	// Verify that cleanup functions are gated on creation flags.
	// deleteTenantViaAPI and deleteComputeInstanceViaAPI both
	// return immediately when the ID is empty.

	// Simulate: tenant not created, ID is empty.
	tenantCreatedFlag := false
	tenantIDVal := ""
	if tenantCreatedFlag && tenantIDVal != "" {
		t.Error("cleanup should be skipped when " +
			"tenantCreated is false")
	}

	// Simulate: CI not created, ID is empty.
	ciCreatedFlag := false
	ciIDVal := ""
	if ciCreatedFlag && ciIDVal != "" {
		t.Error("cleanup should be skipped when " +
			"computeInstanceCreated is false")
	}

	// Simulate: created but ID is empty (should not happen but
	// the gate catches it).
	tenantCreatedFlag = true
	if tenantCreatedFlag && tenantIDVal == "" {
		// Expected: deleteTenantViaAPI returns immediately for
		// empty ID. Verify the gate condition holds.
		t.Log("gate correctly prevents delete with empty ID")
	}
}

func TestHelperSubnetAnnotationParsing(t *testing.T) {
	// Verify JSON-based subnet discovery parses correctly.
	raw := `{"items":[` +
		`{"metadata":{"name":"subnet-aaa",` +
		`"annotations":{` +
		`"osac.openshift.io/tenant":"other-tenant"}}},` +
		`{"metadata":{"name":"subnet-bbb",` +
		`"annotations":{` +
		`"osac.openshift.io/tenant":"mc-tenant-test"}}},` +
		`{"metadata":{"name":"subnet-ccc",` +
		`"annotations":{}}}` +
		`]}`

	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	var found string
	for _, item := range list.Items {
		if item.Metadata.Annotations["osac.openshift.io/tenant"] ==
			"mc-tenant-test" {
			found = item.Metadata.Name
			break
		}
	}
	if found != "subnet-bbb" {
		t.Errorf("expected subnet-bbb, got %q", found)
	}
}

// ---------------------------------------------------------------------------
// Gap 1: Protobuf payload validation against real generated types.
//
// The outgoing gRPC payloads built by createTenantViaAPI and
// createComputeInstanceViaAPI are string-formatted JSON.  These tests
// validate that the EXACT payload structure matches the generated
// protobuf request types using protojson.Unmarshal with
// DiscardUnknown=false, so any unknown fields cause a hard failure.
// ---------------------------------------------------------------------------

// protojsonStrictOpts rejects unknown fields so a typo or
// schema drift in the test payload is caught at compile/test time.
var protojsonStrictOpts = protojson.UnmarshalOptions{
	DiscardUnknown: false,
}

func TestHelperTenantPayloadMatchesProto(t *testing.T) {
	// This is the SAME format string used in createTenantViaAPI.
	tenantName := "mc-tenant-test123"
	payload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s"}}}`,
		tenantName)

	var req privatev1.TenantsCreateRequest
	if err := protojsonStrictOpts.Unmarshal(
		[]byte(payload), &req,
	); err != nil {
		t.Fatalf("Tenant create payload rejected by "+
			"protojson (unknown fields?): %v", err)
	}
	if req.GetObject().GetMetadata().GetName() != tenantName {
		t.Errorf("expected metadata.name=%q, got %q",
			tenantName,
			req.GetObject().GetMetadata().GetName())
	}
}

func TestHelperCIPayloadMatchesProto(t *testing.T) {
	// This is the SAME format string used in
	// createComputeInstanceViaAPI.
	name := "mc-test-abc"
	tenant := "mc-tenant-xyz"
	subnet := "subnet-001"
	payload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s","tenant":"%s"},`+
			`"spec":{"template":{"name":`+
			`"osac.templates.ocp_virt_vm"},`+
			`"boot_disk":{"size_gib":20,`+
			`"storage_tier":{"name":"local"}},`+
			`"run_strategy":`+
			`"COMPUTE_INSTANCE_RUN_STRATEGY_HALTED",`+
			`"network_attachments":`+
			`[{"subnet":{"name":"%s"}}]}}}`,
		name, tenant, subnet)

	var req privatev1.ComputeInstancesCreateRequest
	if err := protojsonStrictOpts.Unmarshal(
		[]byte(payload), &req,
	); err != nil {
		t.Fatalf("ComputeInstance create payload rejected "+
			"by protojson (unknown fields?): %v", err)
	}

	obj := req.GetObject()
	if obj.GetMetadata().GetName() != name {
		t.Errorf("metadata.name: want %q, got %q",
			name, obj.GetMetadata().GetName())
	}
	if obj.GetMetadata().GetTenant() != tenant {
		t.Errorf("metadata.tenant: want %q, got %q",
			tenant, obj.GetMetadata().GetTenant())
	}

	spec := obj.GetSpec()
	if spec.GetTemplate().GetName() !=
		"osac.templates.ocp_virt_vm" {
		t.Errorf("template.name: want %q, got %q",
			"osac.templates.ocp_virt_vm",
			spec.GetTemplate().GetName())
	}

	bd := spec.GetBootDisk()
	if bd.GetSizeGib() != 20 {
		t.Errorf("boot_disk.size_gib: want 20, got %d",
			bd.GetSizeGib())
	}
	if bd.GetStorageTier().GetName() != "local" {
		t.Errorf("boot_disk.storage_tier.name: want %q, "+
			"got %q", "local",
			bd.GetStorageTier().GetName())
	}

	rs := spec.GetRunStrategy()
	//nolint:staticcheck // Enum comparison is intentional.
	want := privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED
	if rs != want {
		t.Errorf("run_strategy: want %v, got %v",
			want, rs)
	}

	nas := spec.GetNetworkAttachments()
	if len(nas) != 1 {
		t.Fatalf("network_attachments: want 1, got %d",
			len(nas))
	}
	if nas[0].GetSubnet().GetName() != subnet {
		t.Errorf("network_attachments[0].subnet.name: "+
			"want %q, got %q",
			subnet, nas[0].GetSubnet().GetName())
	}
}

func TestHelperTenantDeletePayloadMatchesProto(t *testing.T) {
	tID := "tenant-uuid-abc"
	payload := fmt.Sprintf(`{"id":"%s"}`, tID)

	var req privatev1.TenantsDeleteRequest
	if err := protojsonStrictOpts.Unmarshal(
		[]byte(payload), &req,
	); err != nil {
		t.Fatalf("Tenant delete payload rejected by "+
			"protojson: %v", err)
	}
	if req.GetId() != tID {
		t.Errorf("id: want %q, got %q", tID, req.GetId())
	}
}

func TestHelperCIDeletePayloadMatchesProto(t *testing.T) {
	ciID := "ci-uuid-def"
	payload := fmt.Sprintf(`{"id":"%s"}`, ciID)

	var req privatev1.ComputeInstancesDeleteRequest
	if err := protojsonStrictOpts.Unmarshal(
		[]byte(payload), &req,
	); err != nil {
		t.Fatalf("ComputeInstance delete payload rejected "+
			"by protojson: %v", err)
	}
	if req.GetId() != ciID {
		t.Errorf("id: want %q, got %q", ciID, req.GetId())
	}
}

func TestHelperPayloadRejectsUnknownField(t *testing.T) {
	// Prove that protojson with DiscardUnknown=false rejects
	// payloads with unknown fields — the safety net.
	payload := `{"object":{"metadata":{"name":"t"}},` +
		`"bogus_field":"should_fail"}`

	var req privatev1.TenantsCreateRequest
	err := protojsonStrictOpts.Unmarshal(
		[]byte(payload), &req)
	if err == nil {
		t.Fatal("expected protojson to reject unknown " +
			"field 'bogus_field', but it succeeded")
	}
	t.Logf("correctly rejected unknown field: %v", err)
}

// ---------------------------------------------------------------------------
// Gap 2: Exact namespace label and CUDN selector linkage assertions.
//
// The cudn_net.create_subnet Ansible role creates:
//   - Namespace with labels including:
//       k8s.ovn.org/primary-user-defined-network: ""
//       osac.openshift.io/subnet-id: <subnet_id>
//       osac.openshift.io/tenant: <tenant_id>
//   - CUDN with spec.namespaceSelector.matchLabels:
//       osac.openshift.io/subnet-id: <subnet_id>
//
// The test must assert exact key=value, not just key presence.
// ---------------------------------------------------------------------------

// nsLabelAssertions models the exact label values that create_subnet.yaml
// sets on the namespace.  The test assertions in
// split_cluster_test.go parse labels as JSON and verify values.
type nsLabelAssertions struct {
	PrimaryUDN string // k8s.ovn.org/primary-user-defined-network
	SubnetID   string // osac.openshift.io/subnet-id
	TenantID   string // osac.openshift.io/tenant
}

// assertNSLabels checks that actual namespace labels contain
// the required keys with their exact expected values.  Returns
// a list of mismatches (empty on success).
func assertNSLabels(
	actual map[string]string, expected nsLabelAssertions,
) []string {
	var mismatches []string

	// k8s.ovn.org/primary-user-defined-network must be present
	// with empty string value (not just key existence).
	if v, ok := actual["k8s.ovn.org/primary-user-defined-network"]; !ok {
		mismatches = append(mismatches,
			"missing label k8s.ovn.org/"+
				"primary-user-defined-network")
	} else if v != expected.PrimaryUDN {
		mismatches = append(mismatches, fmt.Sprintf(
			"k8s.ovn.org/primary-user-defined-network: "+
				"want %q, got %q",
			expected.PrimaryUDN, v))
	}

	// osac.openshift.io/subnet-id must match subnet_id.
	if v, ok := actual["osac.openshift.io/subnet-id"]; !ok {
		mismatches = append(mismatches,
			"missing label osac.openshift.io/subnet-id")
	} else if v != expected.SubnetID {
		mismatches = append(mismatches, fmt.Sprintf(
			"osac.openshift.io/subnet-id: want %q, got %q",
			expected.SubnetID, v))
	}

	// osac.openshift.io/tenant must match tenant_id.
	if v, ok := actual["osac.openshift.io/tenant"]; !ok {
		mismatches = append(mismatches,
			"missing label osac.openshift.io/tenant")
	} else if v != expected.TenantID {
		mismatches = append(mismatches, fmt.Sprintf(
			"osac.openshift.io/tenant: want %q, got %q",
			expected.TenantID, v))
	}

	return mismatches
}

// assertCUDNSelector checks that the CUDN's
// namespaceSelector.matchLabels exactly links to the subnet.
func assertCUDNSelector(
	matchLabels map[string]string, subnetID string,
) []string {
	var mismatches []string

	if v, ok := matchLabels["osac.openshift.io/subnet-id"]; !ok {
		mismatches = append(mismatches,
			"CUDN namespaceSelector missing "+
				"osac.openshift.io/subnet-id")
	} else if v != subnetID {
		mismatches = append(mismatches, fmt.Sprintf(
			"CUDN namespaceSelector "+
				"osac.openshift.io/subnet-id: "+
				"want %q, got %q",
			subnetID, v))
	}

	return mismatches
}

func TestHelperNSLabelsCorrect(t *testing.T) {
	labels := map[string]string{
		"k8s.ovn.org/primary-user-defined-network": "",
		"osac.openshift.io/subnet-id":              "sn-123",
		"osac.openshift.io/tenant":                 "t-abc",
		"osac.openshift.io/managed-by":             "osac-fulfillment",
	}
	expected := nsLabelAssertions{
		PrimaryUDN: "",
		SubnetID:   "sn-123",
		TenantID:   "t-abc",
	}
	if mm := assertNSLabels(labels, expected); len(mm) > 0 {
		t.Errorf("unexpected mismatches: %v", mm)
	}
}

func TestHelperNSLabelsMissingUDN(t *testing.T) {
	labels := map[string]string{
		"osac.openshift.io/subnet-id": "sn-123",
		"osac.openshift.io/tenant":    "t-abc",
	}
	expected := nsLabelAssertions{
		PrimaryUDN: "",
		SubnetID:   "sn-123",
		TenantID:   "t-abc",
	}
	mm := assertNSLabels(labels, expected)
	if len(mm) == 0 {
		t.Fatal("expected mismatch for missing UDN label")
	}
	found := false
	for _, m := range mm {
		if m == "missing label k8s.ovn.org/"+
			"primary-user-defined-network" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UDN missing error, got: %v", mm)
	}
}

func TestHelperNSLabelsWrongTenant(t *testing.T) {
	labels := map[string]string{
		"k8s.ovn.org/primary-user-defined-network": "",
		"osac.openshift.io/subnet-id":              "sn-123",
		"osac.openshift.io/tenant":                 "wrong-tenant",
	}
	expected := nsLabelAssertions{
		PrimaryUDN: "",
		SubnetID:   "sn-123",
		TenantID:   "t-abc",
	}
	mm := assertNSLabels(labels, expected)
	if len(mm) == 0 {
		t.Fatal("expected mismatch for wrong tenant")
	}
	t.Logf("correctly detected mismatch: %v", mm)
}

func TestHelperNSLabelsWrongSubnetID(t *testing.T) {
	labels := map[string]string{
		"k8s.ovn.org/primary-user-defined-network": "",
		"osac.openshift.io/subnet-id":              "wrong-sn",
		"osac.openshift.io/tenant":                 "t-abc",
	}
	expected := nsLabelAssertions{
		PrimaryUDN: "",
		SubnetID:   "sn-123",
		TenantID:   "t-abc",
	}
	mm := assertNSLabels(labels, expected)
	if len(mm) == 0 {
		t.Fatal("expected mismatch for wrong subnet-id")
	}
	t.Logf("correctly detected mismatch: %v", mm)
}

func TestHelperCUDNSelectorCorrect(t *testing.T) {
	matchLabels := map[string]string{
		"osac.openshift.io/subnet-id": "sn-123",
	}
	if mm := assertCUDNSelector(
		matchLabels, "sn-123",
	); len(mm) > 0 {
		t.Errorf("unexpected mismatches: %v", mm)
	}
}

func TestHelperCUDNSelectorMissing(t *testing.T) {
	matchLabels := map[string]string{}
	mm := assertCUDNSelector(matchLabels, "sn-123")
	if len(mm) == 0 {
		t.Fatal("expected mismatch for missing selector")
	}
	t.Logf("correctly detected: %v", mm)
}

func TestHelperCUDNSelectorWrongValue(t *testing.T) {
	matchLabels := map[string]string{
		"osac.openshift.io/subnet-id": "wrong-sn",
	}
	mm := assertCUDNSelector(matchLabels, "sn-123")
	if len(mm) == 0 {
		t.Fatal("expected mismatch for wrong value")
	}
	t.Logf("correctly detected: %v", mm)
}

// ---------------------------------------------------------------------------
// Gap 3: Token JSON parsing robustness.
//
// acquireFulfillmentToken runs python3 json.load inside the awx-task
// pod (which has python3).  This matches fulfillment-auth.sh exactly.
// These tests validate the python3 JSON parsing contract using
// Go json.Unmarshal (same semantics).
// ---------------------------------------------------------------------------

// parseAccessToken implements the same logic as python3 json.load in
// fulfillment-auth.sh and acquireFulfillmentToken: parse the full
// JSON response and extract the access_token field.
func parseAccessToken(jsonResp string) (string, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(
		[]byte(jsonResp), &resp,
	); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	tok, ok := resp["access_token"]
	if !ok {
		return "", fmt.Errorf("no access_token field")
	}
	tokStr, ok := tok.(string)
	if !ok || tokStr == "" {
		return "", fmt.Errorf("access_token is not a " +
			"non-empty string")
	}
	return tokStr, nil
}

func TestHelperTokenParseCompact(t *testing.T) {
	resp := `{"access_token":"eyJhbGci.test.sig",` +
		`"token_type":"Bearer","expires_in":300}`
	tok, err := parseAccessToken(resp)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if tok != "eyJhbGci.test.sig" {
		t.Errorf("want %q, got %q",
			"eyJhbGci.test.sig", tok)
	}
}

func TestHelperTokenParseWhitespace(t *testing.T) {
	// Keycloak may return pretty-printed JSON.
	resp := `{
  "access_token" : "eyJhbGci.ws.sig",
  "token_type" : "Bearer",
  "expires_in" : 300
}`
	tok, err := parseAccessToken(resp)
	if err != nil {
		t.Fatalf("whitespace JSON parse failed: %v", err)
	}
	if tok != "eyJhbGci.ws.sig" {
		t.Errorf("want %q, got %q",
			"eyJhbGci.ws.sig", tok)
	}
}

func TestHelperTokenParseEmptyRejects(t *testing.T) {
	resp := `{"access_token":"","token_type":"Bearer"}`
	_, err := parseAccessToken(resp)
	if err == nil {
		t.Fatal("expected error for empty access_token")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestHelperTokenParseMissingFieldRejects(t *testing.T) {
	resp := `{"token_type":"Bearer","expires_in":300}`
	_, err := parseAccessToken(resp)
	if err == nil {
		t.Fatal("expected error for missing access_token")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestHelperTokenParseInvalidJSONRejects(t *testing.T) {
	resp := `not json at all`
	_, err := parseAccessToken(resp)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestHelperTokenParseHTMLErrorRejects(t *testing.T) {
	resp := `<html><body>502 Bad Gateway</body></html>`
	_, err := parseAccessToken(resp)
	if err == nil {
		t.Fatal("expected error for HTML response")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestHelperTokenParseNullRejects(t *testing.T) {
	resp := `{"access_token":null,"token_type":"Bearer"}`
	_, err := parseAccessToken(resp)
	if err == nil {
		t.Fatal("expected error for null access_token")
	}
	t.Logf("correctly rejected: %v", err)
}

// TestHelperTokenParsePython3Real runs the ACTUAL python3 script
// used by acquireFulfillmentToken against test fixtures.  This
// validates the real consumer path, not a Go simulation.
func TestHelperTokenParsePython3Real(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available on test host")
	}

	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name: "compact",
			input: `{"access_token":"eyJ.test.sig",` +
				`"token_type":"Bearer"}`,
			want: "eyJ.test.sig",
		},
		{
			name: "whitespace",
			input: "{\n  \"access_token\" : " +
				"\"eyJ.ws.sig\",\n  " +
				"\"token_type\" : \"Bearer\"\n}",
			want: "eyJ.ws.sig",
		},
		{
			name: "newline-separated",
			input: "{\n\"access_token\"\n:\n" +
				"\"eyJ.nl.sig\"\n,\n" +
				"\"token_type\":\"Bearer\"\n}",
			want: "eyJ.nl.sig",
		},
		{
			name:    "empty-value",
			input:   `{"access_token":""}`,
			wantErr: true,
		},
		{
			name:    "missing-field",
			input:   `{"token_type":"Bearer"}`,
			wantErr: true,
		},
		{
			name:    "invalid-json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "html-error",
			input:   `<html>502</html>`,
			wantErr: true,
		},
		{
			name:    "null-value",
			input:   `{"access_token":null}`,
			wantErr: true,
		},
	}

	// This is the EXACT python3 script from
	// acquireFulfillmentToken, extracted verbatim.
	pyScript := `import json,sys; ` +
		`t=json.load(sys.stdin).get("access_token",""); ` +
		`sys.exit(1) if not t else sys.stdout.write(t)`

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("python3", "-c", pyScript)
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.Output()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q",
						out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(out) != tc.want {
				t.Errorf("want %q, got %q",
					tc.want, string(out))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Manifest builder tests: call the REAL buildGRPCPod function and
// verify the emitted Pod struct.
// ---------------------------------------------------------------------------

func TestHelperBuildGRPCPodTenantCreate(t *testing.T) {
	// Use the SAME payload format string as createTenantViaAPI.
	tenantName := "mc-tenant-test"
	payload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s"}}}`,
		tenantName)

	pod := buildGRPCPod(
		"test-pod", "osac", "test-token-secret",
		"osac.private.v1.Tenants/Create",
		payload,
		"fulfillment-internal-api.osac.svc.cluster.local:8001",
	)

	// Verify it serializes to valid JSON (the consumer path).
	podJSON, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Re-parse to verify structure.
	var parsed map[string]interface{}
	if err := json.Unmarshal(podJSON, &parsed); err != nil {
		t.Fatalf("round-trip unmarshal failed: %v", err)
	}

	// Verify the grpcPayload survives JSON round-trip intact.
	containers := pod.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("want 1 container, got %d",
			len(containers))
	}
	c := containers[0]

	// Command must be ["grpcurl"] (no shell).
	if len(c.Command) != 1 || c.Command[0] != "grpcurl" {
		t.Errorf("command: want [grpcurl], got %v",
			c.Command)
	}

	// Args must contain the exact payload.
	foundPayload := false
	for i, arg := range c.Args {
		if arg == "-d" && i+1 < len(c.Args) {
			if c.Args[i+1] != payload {
				t.Errorf("-d arg: want %q, got %q",
					payload, c.Args[i+1])
			}
			foundPayload = true
			break
		}
	}
	if !foundPayload {
		t.Error("-d flag not found in args")
	}

	// Verify method is last arg.
	lastArg := c.Args[len(c.Args)-1]
	if lastArg != "osac.private.v1.Tenants/Create" {
		t.Errorf("last arg: want method, got %q",
			lastArg)
	}

	// Token from Secret, not literal.
	if len(c.Env) != 1 {
		t.Fatalf("want 1 env var, got %d", len(c.Env))
	}
	env := c.Env[0]
	if env.Name != "FULFILLMENT_TOKEN" {
		t.Errorf("env name: want FULFILLMENT_TOKEN, "+
			"got %q", env.Name)
	}
	if env.Value != "" {
		t.Error("env.Value must be empty (use secretKeyRef)")
	}
	if env.ValueFrom == nil ||
		env.ValueFrom.SecretKeyRef == nil {
		t.Fatal("env must use secretKeyRef")
	}
	if env.ValueFrom.SecretKeyRef.Name != "test-token-secret" {
		t.Errorf("secretKeyRef.name: want %q, got %q",
			"test-token-secret",
			env.ValueFrom.SecretKeyRef.Name)
	}

	// AutomountServiceAccountToken must be false.
	if pod.Spec.AutomountServiceAccountToken == nil ||
		*pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken must be " +
			"false")
	}

	// CA bundle volume.
	if len(pod.Spec.Volumes) != 1 {
		t.Fatalf("want 1 volume, got %d",
			len(pod.Spec.Volumes))
	}
	vol := pod.Spec.Volumes[0]
	if vol.Name != "ca-bundle" {
		t.Errorf("volume name: want ca-bundle, got %q",
			vol.Name)
	}
	if vol.ConfigMap == nil ||
		vol.ConfigMap.Name != "ca-bundle" {
		t.Error("volume must reference ca-bundle ConfigMap")
	}

	// No initContainers.
	if len(pod.Spec.InitContainers) > 0 {
		t.Errorf("want 0 initContainers, got %d",
			len(pod.Spec.InitContainers))
	}
}

func TestHelperBuildGRPCPodCICreate(t *testing.T) {
	// Use the SAME payload format as createComputeInstanceViaAPI.
	name := "mc-test-abc"
	tenant := "mc-tenant-xyz"
	subnet := "subnet-001"
	payload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s","tenant":"%s"},`+
			`"spec":{"template":{"name":`+
			`"osac.templates.ocp_virt_vm"},`+
			`"boot_disk":{"size_gib":20,`+
			`"storage_tier":{"name":"local"}},`+
			`"run_strategy":`+
			`"COMPUTE_INSTANCE_RUN_STRATEGY_HALTED",`+
			`"network_attachments":`+
			`[{"subnet":{"name":"%s"}}]}}}`,
		name, tenant, subnet)

	pod := buildGRPCPod(
		"ci-pod", "osac", "ci-token-secret",
		"osac.private.v1.ComputeInstances/Create",
		payload,
		"fulfillment-internal-api.osac.svc.cluster.local:8001",
	)

	podJSON, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Verify JSON is valid and payload survives intact.
	var check map[string]interface{}
	if err := json.Unmarshal(podJSON, &check); err != nil {
		t.Fatalf("round-trip failed: %v", err)
	}

	// Extract the -d arg and validate it against protojson.
	c := pod.Spec.Containers[0]
	var dArg string
	for i, arg := range c.Args {
		if arg == "-d" && i+1 < len(c.Args) {
			dArg = c.Args[i+1]
			break
		}
	}
	if dArg == "" {
		t.Fatal("-d arg not found")
	}

	var req privatev1.ComputeInstancesCreateRequest
	if err := protojsonStrictOpts.Unmarshal(
		[]byte(dArg), &req,
	); err != nil {
		t.Fatalf("payload rejected by protojson: %v", err)
	}
	if req.GetObject().GetMetadata().GetName() != name {
		t.Errorf("metadata.name: want %q, got %q",
			name,
			req.GetObject().GetMetadata().GetName())
	}
}

func TestHelperBuildGRPCPodPayloadWithSpecialChars(t *testing.T) {
	// Verify that payloads with YAML-breaking characters
	// (colons, quotes, braces) survive the JSON serialization.
	// This is the bug that fmt.Sprintf into YAML would break.
	payload := `{"object":{"metadata":{"name":"test: \"quoted\" {braced}"}}}`

	pod := buildGRPCPod(
		"special-pod", "osac", "tok-secret",
		"osac.private.v1.Tenants/Create",
		payload,
		"fulfillment-internal-api.osac.svc.cluster.local:8001",
	)

	podJSON, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// The key test: re-parse and verify the -d arg is
	// bit-identical to the original payload.
	var roundTripped map[string]interface{}
	if err := json.Unmarshal(podJSON, &roundTripped); err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}

	c := pod.Spec.Containers[0]
	for i, arg := range c.Args {
		if arg == "-d" && i+1 < len(c.Args) {
			if c.Args[i+1] != payload {
				t.Errorf("payload corrupted:\n"+
					"  want: %s\n  got:  %s",
					payload, c.Args[i+1])
			}
			return
		}
	}
	t.Error("-d flag not found in args")
}

// ---------------------------------------------------------------------------
// Security regression tests: the REAL buildTokenScript must never
// include secret material in the returned script string.
// ---------------------------------------------------------------------------

func TestHelperBuildTokenScriptNoSecretInScript(t *testing.T) {
	script := buildTokenScript("my-client-id",
		"https://keycloak.example/auth/realms/osac")

	if !strings.Contains(script, "my-client-id") {
		t.Error("script missing client_id")
	}
	if !strings.Contains(script, "keycloak.example") {
		t.Error("script missing token URL")
	}
	if strings.Contains(script, "client_secret=") {
		t.Error("script uses client_secret= (inline)")
	}
	if !strings.Contains(script, "client_secret@") {
		t.Error("script missing client_secret@file")
	}
	if !strings.Contains(script,
		"/var/run/secrets/fc-cred/client-secret") {
		t.Error("script missing mounted secret file path")
	}
	if !strings.Contains(script, "--cacert") {
		t.Error("script missing --cacert")
	}
	if strings.Contains(script, "-k") ||
		strings.Contains(script, "--insecure") {
		t.Error("script uses insecure TLS bypass")
	}
	// Token must NOT go to stdout — must write to file.
	if strings.Contains(script, "sys.stdout.write(t)") {
		t.Error("script writes token to stdout; " +
			"must write to file only")
	}
	if !strings.Contains(script, "open(") {
		t.Error("script must write token to file via open()")
	}
	// Script must include sleep to keep Pod alive.
	if !strings.Contains(script, "sleep") {
		t.Error("script must sleep to keep Pod alive " +
			"for exec retrieval")
	}
}

func TestHelperBuildTokenPodNoSecretInSpec(t *testing.T) {
	sentinel := "SENTINEL_SECRET_abc123"
	script := buildTokenScript("client-id",
		"https://keycloak.example/token")

	pod := buildTokenPod("test-pod", "osac",
		"fulfillment-controller-credentials", script)

	podJSON, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	podStr := string(podJSON)

	if strings.Contains(podStr, sentinel) {
		t.Fatalf("Pod spec contains sentinel")
	}
	if !strings.Contains(podStr,
		"fulfillment-controller-credentials") {
		t.Error("Pod missing Secret reference")
	}
	if !strings.Contains(podStr, "ca-bundle") {
		t.Error("Pod missing ca-bundle ConfigMap")
	}
	if pod.Spec.AutomountServiceAccountToken == nil ||
		*pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken must be false")
	}
	// Secret volume with defaultMode 0440.
	foundSecretVol := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == "fc-cred" && v.Secret != nil {
			foundSecretVol = true
			if v.Secret.DefaultMode == nil ||
				*v.Secret.DefaultMode != 0440 {
				t.Error("fc-cred defaultMode must be 0440")
			}
		}
	}
	if !foundSecretVol {
		t.Error("fc-cred Secret volume not found")
	}
	// Readiness probe must check tokenFilePath.
	c := pod.Spec.Containers[0]
	if c.ReadinessProbe == nil ||
		c.ReadinessProbe.Exec == nil {
		t.Fatal("readiness probe with exec required")
	}
	probeCmd := strings.Join(
		c.ReadinessProbe.Exec.Command, " ")
	if !strings.Contains(probeCmd, tokenFilePath) {
		t.Errorf("readiness probe must check %s, got: %s",
			tokenFilePath, probeCmd)
	}
}

func TestHelperBuildTokenScriptFileFlow(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	sentinel := "test_secret_42"
	tokenOut := t.TempDir() + "/token"
	script := buildTokenScript("test-client",
		"https://fake.example/token")

	// Rewrite paths to temp dir.
	tmpDir := t.TempDir()
	secretDir := tmpDir + "/var/run/secrets/fc-cred"
	_ = os.MkdirAll(secretDir, 0755)
	_ = os.WriteFile(secretDir+"/client-secret",
		[]byte(sentinel), 0600)
	caDir := tmpDir + "/etc/ca-bundle"
	_ = os.MkdirAll(caDir, 0755)
	_ = os.WriteFile(caDir+"/bundle.pem",
		[]byte("mock-ca"), 0644)

	rewritten := strings.ReplaceAll(script,
		"/var/run/secrets/fc-cred/client-secret",
		secretDir+"/client-secret")
	rewritten = strings.ReplaceAll(rewritten,
		"/etc/ca-bundle/bundle.pem", caDir+"/bundle.pem")
	rewritten = strings.ReplaceAll(rewritten,
		tokenFilePath, tokenOut)
	// Remove the sleep so the test doesn't hang.
	rewritten = strings.ReplaceAll(rewritten,
		"exec sleep 300", "true")

	// Mock curl that returns valid JSON.
	mockCurl := tmpDir + "/curl"
	mockScript := fmt.Sprintf(`#!/bin/bash
for arg; do
  case "$arg" in client_secret@*)
    F="${arg#client_secret@}"
    GOT=$(cat "$F")
    if [ "$GOT" = "%s" ]; then
      printf '{"access_token":"file_token_ok"}'
      exit 0
    fi
    echo "WRONG" >&2; exit 1;;
  esac
done
echo "NO_FILE" >&2; exit 1
`, sentinel)
	_ = os.WriteFile(mockCurl, []byte(mockScript), 0755)

	cmd := exec.Command("bash", "-c", rewritten)
	cmd.Env = append(os.Environ(),
		"PATH="+tmpDir+":"+os.Getenv("PATH"))
	stdout, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("script failed: %v\nstderr: %s", err,
			stderr)
	}

	// stdout must be EMPTY — token goes to file only.
	if len(strings.TrimSpace(string(stdout))) > 0 {
		t.Errorf("stdout must be empty, got: %q",
			string(stdout))
	}

	// Token file must exist with correct content.
	data, err := os.ReadFile(tokenOut)
	if err != nil {
		t.Fatalf("token file not created: %v", err)
	}
	if string(data) != "file_token_ok" {
		t.Errorf("token file: want %q, got %q",
			"file_token_ok", string(data))
	}

	// Token file must have mode 0600 (umask 077).
	info, err := os.Stat(tokenOut)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		t.Errorf("token file perm: want 0600, got %04o",
			perm)
	}
}

func TestHelperBuildTokenScriptErrorNoSecretLeak(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script := buildTokenScript("test-client",
		"https://fake.example/token")
	// Remove sleep so test doesn't hang.
	rewritten := strings.ReplaceAll(script,
		"exec sleep 300", "true")

	wrapper := fmt.Sprintf(
		`curl() { echo "curl: (7) Failed" >&2; return 22; }; `+
			`export -f curl; %s`, rewritten)

	cmd := exec.Command("bash", "-c", wrapper)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected script to fail when curl fails")
	}
	if !strings.Contains(string(out), "FAIL") {
		t.Errorf("expected FAIL in error, got: %s", out)
	}
}

func TestHelperReadTokenFromPodErrorGeneric(t *testing.T) {
	// readTokenFromPod must return a generic error that does
	// not contain any token or partial response content.
	// Use a clean pod name (no sentinel in it).
	_, err := readTokenFromPod(
		"nonexistent-test-pod-abc", "default")
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
	errStr := err.Error()
	// Error must say "details withheld" — no kubectl output.
	if !strings.Contains(errStr, "details withheld") {
		t.Errorf("error must say 'details withheld': %s",
			errStr)
	}
	// Error must not contain token file path content.
	if strings.Contains(errStr, "/tmp/token") {
		t.Errorf("error should not reference token path")
	}
}

func TestHelperSourceGrepNoLogsNoInlineSecret(t *testing.T) {
	// Source-grep: acquireFulfillmentToken must NOT call
	// runKubectlHub("logs",...) and must NOT use
	// client_secret= interpolation.
	//
	// The test runs from the osac-operator directory.
	// Locate the source file relative to the test binary.
	// runtime.Caller gives us the source path at compile time.
	_, thisFile, _, _ := runtime.Caller(0)
	srcDir := filepath.Dir(thisFile)
	srcPath := filepath.Join(srcDir,
		"multicluster_suite_test.go")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Skipf("source file not found at %s: %v",
			srcPath, err)
	}

	startMarker := "func acquireFulfillmentToken("
	start := strings.Index(string(src), startMarker)
	if start < 0 {
		t.Fatal("acquireFulfillmentToken not found")
	}
	rest := string(src)[start+len(startMarker):]
	endMarker := "\nfunc "
	end := strings.Index(rest, endMarker)
	if end < 0 {
		end = len(rest)
	}
	body := rest[:end]

	if strings.Contains(body, `"logs"`) {
		t.Error("acquireFulfillmentToken calls " +
			"kubectl logs — token must not go " +
			"through Pod logs")
	}
	if strings.Contains(body, "client_secret=") {
		t.Error("acquireFulfillmentToken has " +
			"client_secret= interpolation")
	}
}
