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

package inventory

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
)

type schemaValidationTestAPI struct {
	fields    []netboxclient.CustomField
	schemaErr error
}

func (*schemaValidationTestAPI) ForEachDevice(
	context.Context,
	netboxclient.DeviceQuery,
	func(netboxclient.Device) (bool, error),
) error {
	return nil
}

func (a *schemaValidationTestAPI) ForEachDeviceCustomField(
	_ context.Context,
	visit func(netboxclient.CustomField) (bool, error),
) error {
	if a.schemaErr != nil {
		return a.schemaErr
	}
	for _, field := range a.fields {
		keepGoing, err := visit(field)
		if err != nil {
			return err
		}
		if !keepGoing {
			return nil
		}
	}
	return nil
}

func (*schemaValidationTestAPI) GetDevice(context.Context, string) (netboxclient.DeviceSnapshot, error) {
	return netboxclient.DeviceSnapshot{}, nil
}

func (*schemaValidationTestAPI) PatchDevice(
	context.Context,
	string,
	netboxclient.DevicePatch,
	string,
) (netboxclient.DeviceSnapshot, error) {
	return netboxclient.DeviceSnapshot{}, nil
}

var _ = Describe("NetBox schema validation", func() {
	Describe("ValidateFixedSchema", func() {
		DescribeTable("accepts valid fixed-field schemas",
			func(activeStatus, unsetDefaults bool) {
				fields := fixedSchemaTestFields()
				if activeStatus {
					for i := range fields {
						fields[i].Status = stringPointer("active")
					}
				}
				if unsetDefaults {
					fields[0].Default = nil
					fields[1].Default = nil
				}
				client := NewNetBoxClient(&schemaValidationTestAPI{fields: fields}, nil, netBoxHostClass)

				Expect(client.ValidateFixedSchema(context.Background())).To(Succeed())
			},
			Entry("status omitted", false, false),
			Entry("active status", true, false),
			Entry("optional defaults omitted", false, true),
		)

		It("returns custom-field discovery errors", func() {
			apiError := errors.New("schema endpoint unavailable")
			client := NewNetBoxClient(&schemaValidationTestAPI{schemaErr: apiError}, nil, netBoxHostClass)

			err := client.ValidateFixedSchema(context.Background())

			Expect(err).To(MatchError(ContainSubstring("failed to discover NetBox device custom-field schema")))
			Expect(errors.Is(err, apiError)).To(BeTrue())
		})

		DescribeTable("rejects incompatible fixed-field schemas",
			func(mutate func([]netboxclient.CustomField) []netboxclient.CustomField, want string) {
				fields := mutate(fixedSchemaTestFields())
				client := NewNetBoxClient(&schemaValidationTestAPI{fields: fields}, nil, netBoxHostClass)

				err := client.ValidateFixedSchema(context.Background())

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(want))
			},
			Entry("missing field", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				return fields[1:]
			}, "osac_managed"),
			Entry("ambiguous field", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				return append(fields, fields[1])
			}, "ambiguous"),
			Entry("wrong object type", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].ObjectTypes = []string{"virtualization.virtualmachine"}
				return fields
			}, "osac_managed"),
			Entry("wrong field type", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].Type = "text"
				return fields
			}, "osac_managed"),
			Entry("required managed field", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].Required = true
				return fields
			}, "osac_managed"),
			Entry("required owner field", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[1].Required = true
				return fields
			}, "osac_instance_id"),
			Entry("unsafe managed default", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].Default = true
				return fields
			}, "osac_managed"),
			Entry("wrong managed default type", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].Default = "false"
				return fields
			}, "osac_managed"),
			Entry("disabled managed filter", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[0].FilterLogic = "disabled"
				return fields
			}, "osac_managed"),
			Entry("loose owner filter", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[1].FilterLogic = "loose"
				return fields
			}, "osac_instance_id"),
			Entry("nonempty owner default", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[1].Default = "claimed"
				return fields
			}, "osac_instance_id"),
			Entry("wrong owner default type", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[1].Default = false
				return fields
			}, "osac_instance_id"),
			Entry("wrong BMC field type", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[2].Type = "integer"
				return fields
			}, "osac_bmc_address"),
			Entry("inactive fixed field", func(fields []netboxclient.CustomField) []netboxclient.CustomField {
				fields[3].Status = stringPointer("deleting")
				return fields
			}, "osac_boot_mac"),
		)
	})

})

func fixedSchemaTestFields() []netboxclient.CustomField {
	return []netboxclient.CustomField{
		{
			Name: "osac_managed", Type: "boolean", Default: false,
			FilterLogic: "exact", ObjectTypes: []string{netBoxDeviceObjectType},
		},
		{
			Name: "osac_instance_id", Type: "text", Default: "",
			FilterLogic: "exact", ObjectTypes: []string{netBoxDeviceObjectType},
		},
		{Name: "osac_bmc_address", Type: "text", ObjectTypes: []string{netBoxDeviceObjectType}},
		{Name: "osac_boot_mac", Type: "text", ObjectTypes: []string{netBoxDeviceObjectType}},
	}
}

func stringPointer(value string) *string {
	return &value
}
