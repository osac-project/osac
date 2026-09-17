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
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func newTestHandler(disabledServices map[string]string) (tap.ServerInHandle, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	handler, err := NewDisabledServiceHandler().
		SetDisabledServices(disabledServices).
		SetMetricsRegisterer(reg).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return handler, reg
}

func startTestServerWithTap(tapHandler tap.ServerInHandle) (*grpc.ClientConn, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	var options []grpc.ServerOption
	if tapHandler != nil {
		options = append(options, grpc.InTapHandle(tapHandler))
	}
	srv := grpc.NewServer(options...)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	Expect(err).ToNot(HaveOccurred())

	cleanup := func() {
		conn.Close()
		srv.Stop()
	}
	return conn, cleanup
}

func invokeMethod(conn *grpc.ClientConn, fullMethod string) error {
	return conn.Invoke(context.Background(), fullMethod, nil, &struct{}{})
}

func getCounterValue(reg *prometheus.Registry, service string) float64 {
	metricFamilies, err := reg.Gather()
	Expect(err).ToNot(HaveOccurred())
	for _, family := range metricFamilies {
		if family.GetName() != "fulfillment_disabled_service_requests_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "service" && label.GetValue() == service {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

var _ = Describe("DisabledServiceTapHandler", func() {
	It("returns Unavailable for a disabled service", func() {
		handler, _ := newTestHandler(map[string]string{
			"/osac.public.v1.Clusters/": "CaaS",
		})
		conn, cleanup := startTestServerWithTap(handler)
		DeferCleanup(cleanup)

		err := invokeMethod(conn, "/osac.public.v1.Clusters/List")
		Expect(err).To(HaveOccurred())

		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
		Expect(st.Message()).To(Equal("the CaaS service is not enabled on this server"))
	})

	It("returns Unavailable for disabled AddOnOperator endpoints", func() {
		handler, _ := newTestHandler(buildDisabledServiceMap(&services.Flags{
			CaaS:  false,
			VMaaS: true,
			BMaaS: true,
		}))
		conn, cleanup := startTestServerWithTap(handler)
		DeferCleanup(cleanup)

		for _, method := range []string{
			"/osac.public.v1.AddOnOperators/List",
			"/osac.private.v1.AddOnOperators/List",
		} {
			err := invokeMethod(conn, method)
			Expect(err).To(HaveOccurred())

			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.Unavailable))
			Expect(st.Message()).To(Equal("the CaaS service is not enabled on this server"))
		}
	})

	It("translates disabled AddOnOperator endpoints to HTTP 503", func() {
		handler, _ := newTestHandler(buildDisabledServiceMap(&services.Flags{
			CaaS:  false,
			VMaaS: true,
			BMaaS: true,
		}))
		conn, cleanup := startTestServerWithTap(handler)
		DeferCleanup(cleanup)

		mux := runtime.NewServeMux()
		err := publicv1.RegisterAddOnOperatorsHandler(context.Background(), mux, conn)
		Expect(err).ToNot(HaveOccurred())
		err = privatev1.RegisterAddOnOperatorsHandler(context.Background(), mux, conn)
		Expect(err).ToNot(HaveOccurred())

		gateway := httptest.NewServer(mux)
		DeferCleanup(gateway.Close)

		for _, path := range []string{
			"/api/fulfillment/v1/add_on_operators",
			"/api/private/v1/add_on_operators",
		} {
			request, err := http.NewRequest(http.MethodGet, gateway.URL+path, nil)
			Expect(err).ToNot(HaveOccurred())
			response, err := http.DefaultClient.Do(request)
			Expect(err).ToNot(HaveOccurred())
			response.Body.Close()
			Expect(response.StatusCode).To(Equal(http.StatusServiceUnavailable))
		}
	})

	It("returns Unimplemented for an unknown service", func() {
		handler, _ := newTestHandler(map[string]string{})
		conn, cleanup := startTestServerWithTap(handler)
		DeferCleanup(cleanup)

		err := invokeMethod(conn, "/osac.public.v1.NonExistent/Get")
		Expect(err).To(HaveOccurred())

		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unimplemented))
	})

	It("increments the Prometheus counter for disabled services", func() {
		handler, reg := newTestHandler(map[string]string{
			"/osac.public.v1.Clusters/":            "CaaS",
			"/osac.private.v1.BareMetalInstances/": "BMaaS",
		})
		conn, cleanup := startTestServerWithTap(handler)
		DeferCleanup(cleanup)

		_ = invokeMethod(conn, "/osac.public.v1.Clusters/List")
		_ = invokeMethod(conn, "/osac.public.v1.Clusters/Get")
		_ = invokeMethod(conn, "/osac.private.v1.BareMetalInstances/List")

		Expect(getCounterValue(reg, "CaaS")).To(Equal(2.0))
		Expect(getCounterValue(reg, "BMaaS")).To(Equal(1.0))
	})

})

var _ = Describe("buildDisabledServiceMap", func() {
	It("returns an empty map when all services are enabled", func() {
		m := buildDisabledServiceMap(&services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true})
		Expect(m).To(BeEmpty())
	})

	It("populates all prefixes when all services are disabled", func() {
		m := buildDisabledServiceMap(&services.Flags{CaaS: false, VMaaS: false, BMaaS: false, MaaS: false})
		totalPrefixes := 0
		for _, prefixes := range disabledServicePrefixes {
			totalPrefixes += len(prefixes)
		}
		totalPrefixes += len(diskImageServicePrefixes)
		Expect(m).To(HaveLen(totalPrefixes))
	})

	It("only includes disabled groups for partial disable", func() {
		m := buildDisabledServiceMap(&services.Flags{CaaS: true, VMaaS: false, BMaaS: true, MaaS: false})
		for prefix := range m {
			Expect(m[prefix]).To(Equal("VMaaS"))
		}
		Expect(m).To(HaveLen(len(disabledServicePrefixes["VMaaS"])))
	})

	It("keeps DiskImages enabled when either VMaaS or BMaaS is enabled", func() {
		for _, flags := range []*services.Flags{
			{CaaS: false, VMaaS: true, BMaaS: false, MaaS: false},
			{CaaS: false, VMaaS: false, BMaaS: true, MaaS: false},
		} {
			m := buildDisabledServiceMap(flags)
			for _, prefix := range diskImageServicePrefixes {
				Expect(m).ToNot(HaveKey(prefix))
			}
		}
	})

	It("disables DiskImages only when both VMaaS and BMaaS are disabled", func() {
		m := buildDisabledServiceMap(&services.Flags{CaaS: true, VMaaS: false, BMaaS: false, MaaS: false})
		for _, prefix := range diskImageServicePrefixes {
			Expect(m).To(HaveKeyWithValue(prefix, "DiskImages"))
		}
	})
})
