package consoleproxy

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericrequest "k8s.io/apiserver/pkg/endpoints/request"
)

const (
	consolePath = "/apis/console.osac.openshift.io/v1alpha1/namespaces/my-ns/computeinstances/my-ci/console"
	vncPath     = "/apis/console.osac.openshift.io/v1alpha1/namespaces/my-ns/computeinstances/my-ci/vnc"
)

func allowAllAuthn() authenticator.Request {
	return authenticator.RequestFunc(func(_ *http.Request) (*authenticator.Response, bool, error) {
		return &authenticator.Response{
			User: &user.DefaultInfo{Name: "test-user", Groups: []string{"system:authenticated"}},
		}, true, nil
	})
}

func rejectAuthn() authenticator.Request {
	return authenticator.RequestFunc(func(_ *http.Request) (*authenticator.Response, bool, error) {
		return nil, false, nil
	})
}

func errorAuthn() authenticator.Request {
	return authenticator.RequestFunc(func(_ *http.Request) (*authenticator.Response, bool, error) {
		return nil, false, http.ErrAbortHandler
	})
}

func allowAllAuthz() authorizer.Authorizer {
	return authorizer.AuthorizerFunc(func(_ context.Context, _ authorizer.Attributes) (authorizer.Decision, string, error) {
		return authorizer.DecisionAllow, "", nil
	})
}

func denyAuthz() authorizer.Authorizer {
	return authorizer.AuthorizerFunc(func(_ context.Context, _ authorizer.Attributes) (authorizer.Decision, string, error) {
		return authorizer.DecisionDeny, "forbidden", nil
	})
}

var _ = Describe("wrapWithAuthFilters", func() {
	DescribeTable("enforces authn/authz",
		func(path string, authn authenticator.Request, authz authorizer.Authorizer,
			wantCode int, wantInnerCalled bool, wantUser string) {
			var innerCalled bool
			var capturedUser string
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				innerCalled = true
				if u, ok := genericrequest.UserFrom(r.Context()); ok {
					capturedUser = u.GetName()
				}
				w.WriteHeader(http.StatusOK)
			})

			handler := wrapWithAuthFilters(inner, authn, authz, nil)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			handler.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(wantCode))
			Expect(innerCalled).To(Equal(wantInnerCalled))
			if wantUser != "" {
				Expect(capturedUser).To(Equal(wantUser))
			}
		},
		Entry("authenticated and authorized",
			consolePath, allowAllAuthn(), allowAllAuthz(),
			http.StatusOK, true, "test-user"),
		Entry("unauthenticated",
			consolePath, rejectAuthn(), allowAllAuthz(),
			http.StatusUnauthorized, false, ""),
		Entry("authentication error",
			consolePath, errorAuthn(), allowAllAuthz(),
			http.StatusUnauthorized, false, ""),
		Entry("authenticated but unauthorized",
			consolePath, allowAllAuthn(), denyAuthz(),
			http.StatusForbidden, false, ""),
		Entry("api discovery passes through auth chain",
			"/apis/console.osac.openshift.io", allowAllAuthn(), allowAllAuthz(),
			http.StatusOK, true, "test-user"),
	)

	Context("request info parsing", func() {
		var captured authorizer.Attributes

		captureAttributes := func(path string) {
			capturingAuthz := authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
				captured = a
				return authorizer.DecisionAllow, "", nil
			})

			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			handler := wrapWithAuthFilters(inner, allowAllAuthn(), capturingAuthz, nil)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			handler.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(captured).NotTo(BeNil())
		}

		DescribeTable("parses subresource requests",
			func(path, wantSubresource string) {
				captureAttributes(path)

				Expect(captured.IsResourceRequest()).To(BeTrue())
				Expect(captured.GetAPIGroup()).To(Equal("console.osac.openshift.io"))
				Expect(captured.GetAPIVersion()).To(Equal("v1alpha1"))
				Expect(captured.GetResource()).To(Equal("computeinstances"))
				Expect(captured.GetSubresource()).To(Equal(wantSubresource))
				Expect(captured.GetNamespace()).To(Equal("my-ns"))
				Expect(captured.GetName()).To(Equal("my-ci"))
				Expect(captured.GetVerb()).To(Equal("get"))
			},
			Entry("console", consolePath, "console"),
			Entry("vnc", vncPath, "vnc"),
		)

		DescribeTable("parses non-resource paths",
			func(path string) {
				captureAttributes(path)

				Expect(captured.IsResourceRequest()).To(BeFalse())
				Expect(captured.GetPath()).To(Equal(path))
			},
			Entry("healthz", "/healthz"),
			Entry("api group discovery", "/apis/console.osac.openshift.io"),
			Entry("api resource list", "/apis/console.osac.openshift.io/v1alpha1"),
		)
	})
})
