/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package database

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/proto/metering"
)

// meteringTables maps the proto message of each resource with metering-tracked fields to the
// table that stores it. Extend together with the metering annotations when a new billable
// resource gets tracked fields; MeteringTableFields fails an unmapped resource, and CheckSchema
// fails a mapped table whose migration has not attached the source-contract triggers.
var meteringTables = map[protoreflect.FullName]string{
	"osac.private.v1.BareMetalInstance": "bare_metal_instances",
	"osac.private.v1.Cluster":           "clusters",
	"osac.private.v1.ComputeInstance":   "compute_instances",
	"osac.private.v1.ExternalIP":        "external_ips",
	"osac.private.v1.NATGateway":        "nat_gateways",
	"osac.private.v1.Volume":            "volumes",
}

// MeteringTableFields returns the metering-tracked fields of each table, from the metering
// annotations on the private protos. Fulfillment's source-contract triggers and the drift test
// compare the installed database configuration against this list.
func MeteringTableFields() (map[string][]metering.Field, error) {
	return meteringTableFields()
}

// meteringTableFields caches the registry: the annotations are compiled in, so the result never
// changes within a process.
var meteringTableFields = sync.OnceValues(loadMeteringTableFields)

func loadMeteringTableFields() (result map[string][]metering.Field, err error) {
	resources, err := metering.Resources()
	if err != nil {
		return nil, err
	}
	result = map[string][]metering.Field{}
	for _, resource := range resources {
		table, ok := meteringTables[resource.Message]
		if !ok {
			return nil, fmt.Errorf("no table mapped for the metered resource '%s'", resource.Message)
		}
		result[table] = resource.Fields
	}
	if len(result) != len(meteringTables) {
		for message, table := range meteringTables {
			if _, ok := result[table]; !ok {
				return nil, fmt.Errorf(
					"table '%s' is mapped for metering but resource '%s' has no tracked fields", table, message,
				)
			}
		}
	}
	return result, nil
}
