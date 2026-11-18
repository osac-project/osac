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

package netboxcontract

import (
	"context"
	"sync"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

// contractFields are the custom-field definitions the adapter's fixed filters
// require. Every one is exact-filtered, which is the schema the adapter assumes
// an administrator has provisioned.
func contractFields() []netboxtest.Field {
	return []netboxtest.Field{
		{Name: "osac_managed", Label: "OSAC managed", Type: "boolean", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_instance_id", Label: "OSAC instance ID", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_bmc_address", Label: "OSAC BMC address", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_boot_mac", Label: "OSAC boot MAC", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_interface_macs", Label: "OSAC interface MACs", Type: "json", FilterLogic: netboxtest.FilterExact},
		{Name: caseFieldName, Label: "Contract case", Type: "text", FilterLogic: netboxtest.FilterExact},
	}
}

// eligibleFields marks a device as an enrolled, unowned pool member for one
// case. osac_instance_id is deliberately absent, which is the representation an
// administrator-created device has.
func eligibleFields(caseValue string) map[string]any {
	return map[string]any{
		"osac_managed": true,
		caseFieldName:  caseValue,
	}
}

// addEligibleDevice registers a staged, managed, unowned device. A nil name
// models a device an administrator created without a name.
func addEligibleDevice(id int, name *string, caseValue string) {
	deviceName := ""
	if name != nil {
		deviceName = *name
	}
	server.AddDevice(netboxtest.Device{
		ID:           id,
		Name:         deviceName,
		Status:       "staged",
		CustomFields: eligibleFields(caseValue),
	})
}

func strPtr(value string) *string { return &value }

// addExactField registers an additional exact-filtered capability field.
func addExactField(name, fieldType string) {
	server.AddField(netboxtest.Field{
		Name:        name,
		Label:       "Contract " + name,
		Type:        fieldType,
		FilterLogic: netboxtest.FilterExact,
	})
}

// claimableFields returns the custom fields a device needs before the adapter
// can resolve Metal3 connection data and credentials for it.
func claimableFields(caseValue string) map[string]any {
	fields := eligibleFields(caseValue)
	fields["osac_bmc_address"] = "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1"
	fields["osac_boot_mac"] = "aa:bb:cc:dd:ee:42"
	return fields
}

// addClaimableDevice registers a staged, managed, unowned device that carries
// valid BMC connection metadata.
func addClaimableDevice(id int, name *string, caseValue string) {
	deviceName := ""
	if name != nil {
		deviceName = *name
	}
	server.AddDevice(netboxtest.Device{
		ID:           id,
		Name:         deviceName,
		Status:       "staged",
		CustomFields: claimableFields(caseValue),
	})
}

// fakeCredentialsReader returns fixed BMC credentials without contacting the
// OSAC Secret API.
type fakeCredentialsReader struct{}

func (fakeCredentialsReader) GetCredentials(context.Context, string) (*credentials.BMC, error) {
	return &credentials.BMC{Username: []byte("bmc-user"), Password: []byte("bmc-pass")}, nil
}

func (fakeCredentialsReader) Close() error { return nil }

// recordingBMHManager records the Metal3 side effects the adapter requests.
type recordingBMHManager struct {
	namespace  string
	mutex      sync.Mutex
	secrets    []string
	created    []baremetalhost.CreateParams
	deletedBMH []string
}

func (m *recordingBMHManager) Namespace() string { return m.namespace }

func (m *recordingBMHManager) CreateBMH(_ context.Context, params baremetalhost.CreateParams) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.created = append(m.created, params)
	return nil
}

func (m *recordingBMHManager) DeleteBMH(_ context.Context, name, _ string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.deletedBMH = append(m.deletedBMH, name)
	return nil
}

func (m *recordingBMHManager) BMHExists(context.Context, string) (bool, error) { return false, nil }

func (m *recordingBMHManager) IsBMHAvailable(context.Context, string) (bool, error) { return true, nil }

func (m *recordingBMHManager) EnsureBMCSecret(_ context.Context, name, _, _, _ string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.secrets = append(m.secrets, name)
	return nil
}

func (m *recordingBMHManager) DeleteBMCSecret(context.Context, string, string) error { return nil }

func (m *recordingBMHManager) GetHardwareNICs(context.Context, string) ([]string, error) {
	return []string{"aa:bb:cc:dd:ee:42"}, nil
}
