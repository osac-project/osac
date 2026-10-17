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
	"fmt"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
)

const netBoxDeviceObjectType = "dcim.device"

type netBoxCustomFieldSchema map[string][]netboxclient.CustomField

type fixedNetBoxField struct {
	name                string
	typeName            string
	requiresExactFilter bool
	expectation         string
}

var fixedNetBoxFields = []fixedNetBoxField{
	{
		name:                "osac_managed",
		typeName:            "boolean",
		requiresExactFilter: true,
		expectation:         "an optional dcim.device Boolean field with default false and exact filtering",
	},
	{
		name:                "osac_instance_id",
		typeName:            "text",
		requiresExactFilter: true,
		expectation:         "an optional dcim.device text field with no non-empty default and exact filtering",
	},
	{
		name:        "osac_bmc_address",
		typeName:    "text",
		expectation: "a dcim.device text field",
	},
	{
		name:        "osac_boot_mac",
		typeName:    "text",
		expectation: "a dcim.device text field",
	},
}

// ValidateFixedSchema checks the integration fields required before the
// NetBox-backed inventory client can start.
func (c *NetBoxClient) ValidateFixedSchema(ctx context.Context) error {
	fields, err := c.discoverCustomFieldSchema(ctx)
	if err != nil {
		return err
	}
	return validateFixedNetBoxSchema(fields)
}

func validateFixedNetBoxSchema(fields netBoxCustomFieldSchema) error {
	for _, spec := range fixedNetBoxFields {
		field, err := uniqueFixedNetBoxFieldDefinition(fields, spec)
		if err != nil {
			return err
		}
		if !validFixedNetBoxField(field, spec) {
			return fmt.Errorf("NetBox custom field %q has an incompatible schema; expected %s", spec.name, spec.expectation)
		}
	}
	return nil
}

func uniqueFixedNetBoxFieldDefinition(
	fields netBoxCustomFieldSchema,
	spec fixedNetBoxField,
) (netboxclient.CustomField, error) {
	matches := fields[spec.name]
	if len(matches) == 0 {
		return netboxclient.CustomField{}, fmt.Errorf("NetBox custom field %q is missing; expected %s", spec.name, spec.expectation)
	}
	if len(matches) > 1 {
		return netboxclient.CustomField{}, fmt.Errorf("NetBox custom field %q is ambiguous; expected %s", spec.name, spec.expectation)
	}
	return matches[0], nil
}

func validFixedNetBoxField(field netboxclient.CustomField, spec fixedNetBoxField) bool {
	return field.Type == spec.typeName &&
		hasObjectType(field.ObjectTypes, netBoxDeviceObjectType) &&
		(field.Status == nil || *field.Status == "active") &&
		(!spec.requiresExactFilter || field.FilterLogic == "exact") &&
		validFixedNetBoxDefault(field, spec.name)
}

func validFixedNetBoxDefault(field netboxclient.CustomField, name string) bool {
	switch name {
	case "osac_managed":
		return !field.Required && isFalseOrUnset(field.Default)
	case "osac_instance_id":
		return !field.Required && isEmptyOrUnset(field.Default)
	default:
		return true
	}
}

func isFalseOrUnset(value any) bool {
	if value == nil {
		return true
	}
	defaultValue, ok := value.(bool)
	return ok && !defaultValue
}

func isEmptyOrUnset(value any) bool {
	if value == nil {
		return true
	}
	defaultValue, ok := value.(string)
	return ok && defaultValue == ""
}

func hasObjectType(objectTypes []string, expected string) bool {
	for _, objectType := range objectTypes {
		if objectType == expected {
			return true
		}
	}
	return false
}

func (c *NetBoxClient) discoverCustomFieldSchema(
	ctx context.Context,
) (netBoxCustomFieldSchema, error) {
	fields := make(netBoxCustomFieldSchema)
	err := c.api.ForEachDeviceCustomField(ctx, func(field netboxclient.CustomField) (bool, error) {
		fields[field.Name] = append(fields[field.Name], field)
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to discover NetBox device custom-field schema: %w", err)
	}
	return fields, nil
}
