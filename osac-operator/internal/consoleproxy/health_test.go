package consoleproxy

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("Probe Mux", func() {
	DescribeTable("returns the expected status code",
		func(path string, prepare func(*probeState), want int) {
			probes := newProbeState()
			if prepare != nil {
				prepare(probes)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			newProbeMux(probes).ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(want))
		},
		Entry("healthz is always healthy",
			"/healthz", nil, http.StatusOK),
		Entry("livez is always healthy",
			"/livez", nil, http.StatusOK),
		Entry("readyz is unavailable before ready",
			"/readyz", nil, http.StatusServiceUnavailable),
		Entry("readyz is healthy after ready",
			"/readyz",
			func(probes *probeState) { probes.MarkReady() },
			http.StatusOK),
		Entry("readyz is unavailable while shutting down",
			"/readyz",
			func(probes *probeState) {
				probes.MarkReady()
				probes.MarkShuttingDown()
			},
			http.StatusServiceUnavailable),
	)
})

var _ = Describe("API Mux", func() {
	DescribeTable("does not serve probe routes",
		func(path string) {
			handler := (&Server{}).newAPIMux()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
		},
		Entry("healthz", "/healthz"),
		Entry("livez", "/livez"),
		Entry("readyz", "/readyz"),
	)
})
