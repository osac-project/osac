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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
)

func newTestCounter(reg *prometheus.Registry) *prometheus.CounterVec {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fulfillment_disabled_service_requests_total",
	}, []string{"service"})
	reg.MustRegister(counter)
	return counter
}

func startTestServer(handler grpc.StreamHandler) (*grpc.ClientConn, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	srv := grpc.NewServer(grpc.UnknownServiceHandler(handler))
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

func getCounterValue(counter *prometheus.CounterVec, labels ...string) float64 {
	m := &dto.Metric{}
	c, err := counter.GetMetricWithLabelValues(labels...)
	if err != nil {
		return 0
	}
	_ = c.Write(m)
	if m.Counter == nil {
		return 0
	}
	return *m.Counter.Value
}

var _ = Describe("UnknownServiceHandler", func() {
	It("returns Unavailable for a disabled service", func() {
		reg := prometheus.NewRegistry()
		counter := newTestCounter(reg)
		flags := &services.Flags{CaaS: false, VMaaS: true, BMaaS: true, MaaS: false}
		handler := NewUnknownServiceHandler(flags, counter)
		conn, cleanup := startTestServer(handler)
		DeferCleanup(cleanup)

		err := invokeMethod(conn, "/osac.public.v1.Clusters/List")
		Expect(err).To(HaveOccurred())

		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
		Expect(st.Message()).To(Equal("the CaaS service is not enabled on this server"))
	})

	It("returns Unimplemented for an unknown service", func() {
		reg := prometheus.NewRegistry()
		counter := newTestCounter(reg)
		flags := &services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true}
		handler := NewUnknownServiceHandler(flags, counter)
		conn, cleanup := startTestServer(handler)
		DeferCleanup(cleanup)

		err := invokeMethod(conn, "/osac.public.v1.NonExistent/Get")
		Expect(err).To(HaveOccurred())

		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unimplemented))
	})

	It("increments the Prometheus counter for disabled services", func() {
		reg := prometheus.NewRegistry()
		counter := newTestCounter(reg)
		flags := &services.Flags{CaaS: false, VMaaS: true, BMaaS: false, MaaS: false}
		handler := NewUnknownServiceHandler(flags, counter)
		conn, cleanup := startTestServer(handler)
		DeferCleanup(cleanup)

		_ = invokeMethod(conn, "/osac.public.v1.Clusters/List")
		_ = invokeMethod(conn, "/osac.public.v1.Clusters/Get")
		_ = invokeMethod(conn, "/osac.private.v1.BareMetalInstances/List")

		Expect(getCounterValue(counter, "CaaS")).To(Equal(2.0))
		Expect(getCounterValue(counter, "BMaaS")).To(Equal(1.0))
	})

	It("returns Unimplemented for an enabled but not registered service", func() {
		reg := prometheus.NewRegistry()
		counter := newTestCounter(reg)
		flags := &services.Flags{CaaS: false, VMaaS: true, BMaaS: true, MaaS: false}
		handler := NewUnknownServiceHandler(flags, counter)
		conn, cleanup := startTestServer(handler)
		DeferCleanup(cleanup)

		err := invokeMethod(conn, "/osac.public.v1.ComputeInstances/List")
		Expect(err).To(HaveOccurred())

		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unimplemented))
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
		Expect(m).To(HaveLen(totalPrefixes))
	})

	It("only includes disabled groups for partial disable", func() {
		m := buildDisabledServiceMap(&services.Flags{CaaS: true, VMaaS: false, BMaaS: true, MaaS: false})
		for prefix := range m {
			Expect(m[prefix]).To(Equal("VMaaS"))
		}
		Expect(m).To(HaveLen(len(disabledServicePrefixes["VMaaS"])))
	})
})
