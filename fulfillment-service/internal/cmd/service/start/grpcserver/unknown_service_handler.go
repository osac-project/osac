/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
)

// disabledServicePrefixes maps each service flag to its set of gRPC service full-name prefixes.
// When a flag is false, all prefixes in that group are added to the disabled set.
var disabledServicePrefixes = map[string][]string{
	"CaaS": {
		"/osac.public.v1.ClusterTemplates/",
		"/osac.public.v1.ClusterCatalogItems/",
		"/osac.public.v1.Clusters/",
		"/osac.public.v1.ClusterVersions/",
		"/osac.private.v1.ClusterTemplates/",
		"/osac.private.v1.ClusterCatalogItems/",
		"/osac.private.v1.Clusters/",
		"/osac.private.v1.ClusterVersions/",
	},
	"VMaaS": {
		"/osac.public.v1.ComputeInstanceTemplates/",
		"/osac.public.v1.ComputeInstanceCatalogItems/",
		"/osac.public.v1.ComputeInstances/",
		"/osac.public.v1.DiskImages/",
		"/osac.public.v1.ConsoleSessions/",
		"/osac.public.v1.InstanceTypes/",
		"/osac.private.v1.ComputeInstanceTemplates/",
		"/osac.private.v1.ComputeInstanceCatalogItems/",
		"/osac.private.v1.ComputeInstances/",
		"/osac.private.v1.DiskImages/",
		"/osac.private.v1.InstanceTypes/",
		"/osac.private.v1.Volumes/",
	},
	"BMaaS": {
		"/osac.public.v1.BareMetalInstanceTemplates/",
		"/osac.public.v1.BareMetalInstanceCatalogItems/",
		"/osac.public.v1.BareMetalInstances/",
		"/osac.public.v1.BareMetalInstanceTypes/",
		"/osac.private.v1.BareMetalInstanceTemplates/",
		"/osac.private.v1.BareMetalInstanceCatalogItems/",
		"/osac.private.v1.BareMetalInstances/",
		"/osac.private.v1.BareMetalInstanceTypes/",
	},
}

// buildDisabledServiceMap builds a map from gRPC method prefix to the service group name
// (e.g. "CaaS") for every service that is currently disabled.
func buildDisabledServiceMap(svcFlags *services.Flags) map[string]string {
	disabled := make(map[string]string)
	flagValues := map[string]bool{
		"CaaS":  svcFlags.CaaS,
		"VMaaS": svcFlags.VMaaS,
		"BMaaS": svcFlags.BMaaS,
	}
	for group, prefixes := range disabledServicePrefixes {
		if !flagValues[group] {
			for _, prefix := range prefixes {
				disabled[prefix] = group
			}
		}
	}
	return disabled
}

// NewUnknownServiceHandler returns a grpc.StreamHandler that returns codes.Unavailable for
// known-but-disabled services and codes.Unimplemented for genuinely unknown services.
func NewUnknownServiceHandler(
	svcFlags *services.Flags,
	counter *prometheus.CounterVec,
) grpc.StreamHandler {
	disabledMap := buildDisabledServiceMap(svcFlags)

	return func(_ interface{}, stream grpc.ServerStream) error {
		method, ok := grpc.MethodFromServerStream(stream)
		if !ok {
			return status.Error(codes.Unimplemented, "unknown service")
		}

		for prefix, group := range disabledMap {
			if strings.HasPrefix(method, prefix) {
				counter.WithLabelValues(group).Inc()
				return status.Errorf(
					codes.Unavailable,
					"the %s service is not enabled on this server",
					group,
				)
			}
		}

		return status.Errorf(codes.Unimplemented, "unknown service %s", method)
	}
}
