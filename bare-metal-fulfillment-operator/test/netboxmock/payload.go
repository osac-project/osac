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

// Package netboxmock configures the NetBox device API used by BMF tests.
package netboxmock

import (
	"maps"
	"strconv"
	"strings"
)

// DevicePayload returns the dcim.device response shape the generated SDK
// requires, so tests share one fixture instead of maintaining their own copies.
//
//nolint:goconst // Keep the JSON keys visible in the wire fixture.
func DevicePayload(id int, name, status string, customFields map[string]any) map[string]any {
	fields := maps.Clone(customFields)
	label := status
	if status != "" {
		label = strings.ToUpper(status[:1]) + status[1:]
	}
	return map[string]any{
		"id":      id,
		"url":     "/api/dcim/devices/" + strconv.Itoa(id) + "/",
		"display": name,
		"name":    name,
		"device_type": map[string]any{
			"id":      1,
			"url":     "/api/dcim/device-types/1/",
			"display": "Test device type",
			"manufacturer": map[string]any{
				"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer",
				"name": "Test manufacturer", "slug": "test-manufacturer",
			},
			"model": "Test model",
			"slug":  "test-device-type",
		},
		"role": map[string]any{
			"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server",
			"name": "Server", "slug": "server", "_depth": 0,
		},
		"site": map[string]any{
			"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site",
			"name": "Test site", "slug": "test-site",
		},
		"status":                    map[string]any{"value": status, "label": label},
		"custom_fields":             fields,
		"console_port_count":        0,
		"console_server_port_count": 0,
		"power_port_count":          0,
		"power_outlet_count":        0,
		"front_port_count":          0,
		"rear_port_count":           0,
		"device_bay_count":          0,
		"module_bay_count":          0,
		"inventory_item_count":      0,
	}
}
