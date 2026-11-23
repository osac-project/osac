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
	"crypto/subtle"
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
	if first == "" || rotated == "" || subtle.ConstantTimeCompare([]byte(first), []byte(rotated)) == 1 {
		log.Fatal("the NetBox test fixture requires two distinct tokens")
	}
	mock := netboxmock.New(first, rotated)
	mock.AddDevice(deviceID, "staged", map[string]any{
		"osac_managed":     true,
		"osac_bmc_address": "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
		"osac_boot_mac":    "aa:bb:cc:dd:ee:42",
		"fixture":          "kind",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /fixture-state", func(w http.ResponseWriter, _ *http.Request) {
		device := mock.Device(deviceID)
		patches := mock.RequestsFor(http.MethodPatch, fmt.Sprintf("/api/dcim/devices/%d/", deviceID))
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"deviceID": deviceID, "status": device.Status, "owner": device.CustomFields["osac_instance_id"],
			"rotatedClaims": countRotatedClaims(patches),
		}); err != nil {
			log.Print(err)
		}
	})
	mux.Handle("/", mock)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

// countRotatedClaims counts successful claims authenticated with the rotated token.
func countRotatedClaims(requests []netboxmock.Request) int {
	const rotatedTokenIndex = 1
	count := 0
	for _, request := range requests {
		if request.Claimed && request.TokenIndex == rotatedTokenIndex {
			count++
		}
	}
	return count
}
