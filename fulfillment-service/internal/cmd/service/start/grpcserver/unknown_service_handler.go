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
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
)

// disabledServicePrefixes maps each service flag to its set of gRPC service full-name prefixes.
// When a flag is false, all prefixes in that group are added to the disabled set.
var disabledServicePrefixes = map[string][]string{
	"CaaS": {
		"/osac.public.v1.ClusterTemplates/",
		"/osac.public.v1.AddOnOperators/",
		"/osac.public.v1.ClusterCatalogItems/",
		"/osac.public.v1.Clusters/",
		"/osac.public.v1.ClusterVersions/",
		"/osac.private.v1.ClusterTemplates/",
		"/osac.private.v1.AddOnOperators/",
		"/osac.private.v1.ClusterCatalogItems/",
		"/osac.private.v1.Clusters/",
		"/osac.private.v1.ClusterVersions/",
	},
	"VMaaS": {
		"/osac.public.v1.ComputeInstanceTemplates/",
		"/osac.public.v1.ComputeInstanceCatalogItems/",
		"/osac.public.v1.ComputeInstances/",
		"/osac.public.v1.ConsoleSessions/",
		"/osac.public.v1.InstanceTypes/",
		"/osac.private.v1.ComputeInstanceTemplates/",
		"/osac.private.v1.ComputeInstanceCatalogItems/",
		"/osac.private.v1.ComputeInstances/",
		"/osac.private.v1.InstanceTypes/",
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

// diskImageServicePrefixes contains the services shared by VMaaS and BMaaS. They are disabled only when neither
// workload service is enabled.
var diskImageServicePrefixes = []string{
	"/osac.public.v1.DiskImages/",
	"/osac.private.v1.DiskImages/",
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
	if !svcFlags.VMaaS && !svcFlags.BMaaS {
		for _, prefix := range diskImageServicePrefixes {
			disabled[prefix] = "DiskImages"
		}
	}
	return disabled
}

// DisabledServiceHandlerBuilder contains the data and logic needed to create a disabled-service handler. Don't create
// objects of this type directly; use NewDisabledServiceHandler instead.
type DisabledServiceHandlerBuilder struct {
	disabledServices  map[string]string
	metricsRegisterer prometheus.Registerer
}

// disabledServiceHandler handles requests to known-but-disabled services before they enter the interceptor chain.
type disabledServiceHandler struct {
	disabledServices map[string]string
	counter          *prometheus.CounterVec
}

// NewDisabledServiceHandler creates a builder that can be used to configure and create a disabled-service handler.
func NewDisabledServiceHandler() *DisabledServiceHandlerBuilder {
	return &DisabledServiceHandlerBuilder{}
}

// SetDisabledServices sets the gRPC method prefixes for disabled services and their service group names. This is
// mandatory, but an empty map is valid when all services are enabled.
func (b *DisabledServiceHandlerBuilder) SetDisabledServices(value map[string]string) *DisabledServiceHandlerBuilder {
	b.disabledServices = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the handler metrics. This is mandatory.
func (b *DisabledServiceHandlerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *DisabledServiceHandlerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build uses the data stored in the builder to create a disabled-service tap handler.
func (b *DisabledServiceHandlerBuilder) Build() (result tap.ServerInHandle, err error) {
	if b.disabledServices == nil {
		err = errors.New("disabled services are mandatory")
		return
	}
	if b.metricsRegisterer == nil {
		err = errors.New("metrics registerer is mandatory")
		return
	}

	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fulfillment_disabled_service_requests_total",
		Help: "Total requests to disabled services.",
	}, []string{"service"})
	if err = b.metricsRegisterer.Register(counter); err != nil {
		var registered prometheus.AlreadyRegisteredError
		if !errors.As(err, &registered) {
			return
		}
		var ok bool
		counter, ok = registered.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			err = errors.New("registered disabled-service metric has unexpected type")
			return
		}
	}

	handler := &disabledServiceHandler{
		disabledServices: maps.Clone(b.disabledServices),
		counter:          counter,
	}
	result = handler.handle
	return
}

// handle rejects requests to known-but-disabled services before they enter the interceptor chain.
func (h *disabledServiceHandler) handle(ctx context.Context, info *tap.Info) (context.Context, error) {
	for prefix, group := range h.disabledServices {
		if strings.HasPrefix(info.FullMethodName, prefix) {
			h.counter.WithLabelValues(group).Inc()
			return ctx, status.Errorf(
				codes.Unavailable,
				"the %s service is not enabled on this server",
				group,
			)
		}
	}
	return ctx, nil
}
