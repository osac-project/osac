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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// The result goldens in tests/contract/network-task/results are the job
// artifacts a manager returns. The osac-aap tests produce the same files from
// the real roles; the operator parsers must accept them. The controller specs
// (networking_wire_golden_test.go) feed the same files through the engine.
const wireResultsDir = "../../../tests/contract/network-task/results"

func wireResult(t *testing.T, name string) Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(wireResultsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := readArtifacts(json.RawMessage(data))
	if artifacts.result == nil {
		t.Fatalf("%s has no osac_result", name)
	}
	return artifacts.result
}

func TestParsersAcceptTheWireResultGoldens(t *testing.T) {
	id, l3, err := ParseVirtualNetworkResult(wireResult(t, "create_virtual_network.fabric.json"), "evpn")
	if err != nil || id != "101" || l3 != 10001 {
		t.Fatalf("virtual network result: %q %d %v", id, l3, err)
	}
	segment, err := ParseNetworkSegment(wireResult(t, "create_subnet.fabric.json"), "evpn")
	if err != nil || *segment != (v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10101}) {
		t.Fatalf("subnet result: %+v %v", segment, err)
	}
	address, err := ParseExternalIP(wireResult(t, "create_external_ip.fabric.json"), "203.0.113.0/30")
	if err != nil || address != "203.0.113.1" {
		t.Fatalf("external ip result: %q %v", address, err)
	}
}
