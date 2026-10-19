/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"sort"
	"strconv"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func computeInstanceCoverageRequests(instance *privatev1.ComputeInstance) []CatalogPricingRequest {
	request := CatalogPricingRequest{
		Service:    billing.ServiceComputeInstance,
		Dimensions: make(map[string]string),
		Supported:  instance != nil && instance.GetSpec() != nil,
	}
	if !request.Supported {
		return []CatalogPricingRequest{request}
	}
	spec := instance.GetSpec()
	addNamedReference(request.Dimensions, "instance_type", spec.GetInstanceType())
	additionalDisks := spec.GetAdditionalDisks()
	if len(additionalDisks) == 0 {
		return []CatalogPricingRequest{request}
	}
	requests := []CatalogPricingRequest{request}
	for _, disk := range additionalDisks {
		diskRequest := CatalogPricingRequest{
			Service:     request.Service,
			Dimensions:  make(map[string]string, 2),
			Supported:   request.Supported && disk != nil,
			Description: "Compute instance additional disk",
		}
		if disk != nil {
			if disk.GetSizeGib() > 0 {
				diskRequest.Dimensions["additional_disk_size_gib"] = strconv.FormatInt(int64(disk.GetSizeGib()), 10)
			}
			addNamedReference(diskRequest.Dimensions, "additional_storage_tier", disk.GetStorageTier())
		}
		requests = append(requests, diskRequest)
	}
	return requests
}

func clusterCoverageRequests(cluster *privatev1.Cluster) []CatalogPricingRequest {
	base := CatalogPricingRequest{
		Service:    billing.ServiceCluster,
		Dimensions: make(map[string]string),
		Supported:  cluster != nil && cluster.GetSpec() != nil,
	}
	if !base.Supported {
		return []CatalogPricingRequest{base}
	}
	spec := cluster.GetSpec()
	if len(spec.GetNodeSets()) == 0 {
		base.Supported = false
		return []CatalogPricingRequest{base}
	}
	names := make([]string, 0, len(spec.GetNodeSets()))
	for name := range spec.GetNodeSets() {
		names = append(names, name)
	}
	sort.Strings(names)
	requests := make([]CatalogPricingRequest, 0, len(spec.GetNodeSets()))
	for _, name := range names {
		nodeSet := spec.GetNodeSets()[name]
		request := CatalogPricingRequest{
			Service:      base.Service,
			Dimensions:   make(map[string]string, 2),
			Supported:    base.Supported && nodeSet != nil,
			Description:  name,
			PriceMayVary: true,
		}
		if nodeSet != nil {
			addNamedReference(request.Dimensions, "baremetal_instance_type", nodeSet.GetBaremetalInstanceType())
			if nodeSet.GetSize() > 0 {
				request.Dimensions["node_count"] = strconv.FormatInt(int64(nodeSet.GetSize()), 10)
			} else {
				request.Supported = false
			}
			if request.Dimensions["baremetal_instance_type"] == "" {
				request.Supported = false
			}
		}
		requests = append(requests, request)
	}
	return requests
}

type namedResourceReference interface {
	GetName() string
}

func addNamedReference(dimensions map[string]string, dimension string, reference namedResourceReference) {
	if reference != nil && reference.GetName() != "" {
		dimensions[dimension] = reference.GetName()
	}
}
