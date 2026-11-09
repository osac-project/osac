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

package netboxclient

import (
	"net/http"
	"strconv"
	"strings"

	netbox "github.com/netbox-community/go-netbox/v4"
)

// NewClientForTest creates a Client with an injected HTTP client and API token.
func NewClientForTest(httpClient *http.Client, endpoint, token string) *Client {
	testClient := *httpClient
	transport := testClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	testClient.Transport = &requestExtensionTransport{base: transport}
	testClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	configuration := netbox.NewConfiguration()
	configuration.Servers[0].URL = strings.TrimRight(endpoint, "/")
	configuration.Debug = false
	configuration.AddDefaultHeader("Authorization", netBoxAuthorizationHeader(token))
	configuration.HTTPClient = &testClient
	return &Client{api: netbox.NewAPIClient(configuration)}
}

// DevicePayloadForTest returns the dcim.device response shape the generated SDK
// requires, so tests share one fixture instead of maintaining their own copies.
func DevicePayloadForTest(id int, name, status string, customFields map[string]interface{}) map[string]interface{} {
	fields := make(map[string]interface{}, len(customFields))
	for key, value := range customFields {
		fields[key] = value
	}
	return map[string]interface{}{
		"id":      id,
		"url":     "/api/dcim/devices/" + strconv.Itoa(id) + "/",
		"display": name,
		"name":    name,
		"device_type": map[string]interface{}{
			"id":      1,
			"url":     "/api/dcim/device-types/1/",
			"display": "Test device type",
			"manufacturer": map[string]interface{}{
				"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer",
				"name": "Test manufacturer", "slug": "test-manufacturer",
			},
			"model": "Test model",
			"slug":  "test-device-type",
		},
		"role":                      map[string]interface{}{"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server", "name": "Server", "slug": "server", "_depth": 0},
		"site":                      map[string]interface{}{"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site", "name": "Test site", "slug": "test-site"},
		"status":                    map[string]interface{}{"value": status, "label": strings.ToUpper(status[:1]) + status[1:]},
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
