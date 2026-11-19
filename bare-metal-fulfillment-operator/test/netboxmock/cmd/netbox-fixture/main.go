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

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxmock"
)

func main() {
	deviceID := int(time.Now().UnixMilli()%1_000_000_000 + 1)
	first := os.Getenv("NETBOX_TOKEN")
	rotated := os.Getenv("NETBOX_ROTATED_TOKEN")
	if first == "" || rotated == "" || first == rotated {
		log.Fatal("the NetBox test fixture requires two distinct tokens")
	}
	mock := netboxmock.NewHandler()
	mock.SetAuth("Bearer", first, rotated)
	for name, fieldType := range map[string]string{
		"osac_managed": "boolean", "osac_instance_id": "text", "fixture": "text",
	} {
		mock.AddField(netboxmock.Field{
			Name: name, Type: fieldType, FilterLogic: netboxmock.FilterExact,
			ObjectTypes: []string{"dcim.device"},
		})
	}
	mock.AddDevice(netboxmock.Device{
		ID: deviceID, Name: "Kind fixture", Status: "staged",
		CustomFields: map[string]any{
			"osac_managed":     true,
			"osac_bmc_address": "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
			"osac_boot_mac":    "aa:bb:cc:dd:ee:42",
			"fixture":          "kind",
		},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /fixture-state", func(w http.ResponseWriter, _ *http.Request) {
		device, _ := mock.Device(deviceID)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"deviceID": deviceID, "status": device.Status, "owner": device.CustomFields["osac_instance_id"],
			"rotatedClaims": countRotatedClaims(mock.Requests(), deviceID),
		}); err != nil {
			log.Print(err)
		}
	})
	mux.Handle("/", mock)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

// countRotatedClaims counts successful claim PATCHes authenticated with the
// second token supplied to SetAuth.
func countRotatedClaims(requests []netboxmock.Request, deviceID int) int {
	const rotatedTokenIndex = 1
	devicePath := fmt.Sprintf("/api/dcim/devices/%d/", deviceID)
	count := 0
	for _, request := range requests {
		if request.TokenIndex != rotatedTokenIndex || request.Method != http.MethodPatch ||
			request.Path != devicePath || request.StatusCode != http.StatusOK {
			continue
		}
		var patch struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(request.Body, &patch) == nil && patch.Status == "active" {
			count++
		}
	}
	return count
}
