package consoleproxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("API Mux", func() {
	var mux http.Handler

	BeforeEach(func() {
		s := &Server{
			logger:         discardLogger,
			configResolver: fakeConfigResolver{err: errors.New("no backend")},
		}
		mux = s.newAPIMux()
	})

	DescribeTable("routes requests correctly",
		func(method, path string, wantCode int, wantKind string) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Accept", "application/json")

			mux.ServeHTTP(rec, req)

			if wantCode != 0 {
				Expect(rec.Code).To(Equal(wantCode))
			}

			if wantKind != "" {
				var resp struct {
					Kind string `json:"kind"`
				}
				Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
				Expect(resp.Kind).To(Equal(wantKind))
			}
		},
		Entry("routes api group list",
			http.MethodGet, "/apis",
			http.StatusOK, "APIGroupList"),
		Entry("routes api group",
			http.MethodGet, "/apis/"+apiGroup,
			http.StatusOK, "APIGroup"),
		Entry("routes api resource list",
			http.MethodGet, "/apis/"+apiGroup+"/"+apiVersion,
			http.StatusOK, "APIResourceList"),
		Entry("unknown path returns 404",
			http.MethodGet, "/apis/unknown.group/v1",
			http.StatusNotFound, ""),
		Entry("wrong method returns 405",
			http.MethodPost, "/apis",
			http.StatusMethodNotAllowed, ""),
	)

	DescribeTable("routes subresource paths to handler",
		func(subresource string) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				"/apis/"+apiGroup+"/"+apiVersion+"/namespaces/ns/computeinstances/ci/"+subresource, nil)
			req.Header.Set("Accept", "application/json")

			mux.ServeHTTP(rec, req)

			Expect(rec.Code).NotTo(Equal(http.StatusNotFound))
		},
		Entry("console", "console"),
		Entry("vnc", "vnc"),
	)
})
