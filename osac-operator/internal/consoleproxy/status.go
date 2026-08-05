package consoleproxy

import (
	"fmt"
	"io"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/client-go/kubernetes/scheme"
)

var (
	proxyGroupVersion = schema.GroupVersion{Group: apiGroup, Version: apiVersion}
	computeInstanceGR = schema.GroupResource{Group: apiGroup, Resource: "computeinstances"}
)

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	responsewriters.ErrorNegotiated(err, scheme.Codecs, proxyGroupVersion, w, r)
}

func writeObject(w http.ResponseWriter, r *http.Request, statusCode int, obj runtime.Object) {
	responsewriters.WriteObjectNegotiated(
		scheme.Codecs,
		negotiation.DefaultEndpointRestrictions,
		schema.GroupVersion{},
		w,
		r,
		statusCode,
		obj,
		false,
	)
}

func newResolveConfigStatusError(err error) error {
	return apierrors.NewServiceUnavailable(fmt.Sprintf("failed to resolve cluster configuration: %v", err))
}

func newComputeInstanceLookupStatusError(name string, err error) error {
	if apierrors.IsNotFound(err) {
		return apierrors.NewNotFound(computeInstanceGR, name)
	}
	return apierrors.NewInternalError(fmt.Errorf("failed to get compute instance %q: %w", name, err))
}

func newVMReferenceStatusError(name, message string) error {
	return apierrors.NewServiceUnavailable(fmt.Sprintf("compute instance %q: %s", name, message))
}

func newConnectStatusError(dialErr error, resp *http.Response) error {
	if resp != nil {
		return &upstreamError{resp: resp}
	}
	return apierrors.NewServiceUnavailable(fmt.Sprintf("failed to connect to VM: %v", dialErr))
}

func newConfigStatusError(message string, err error) error {
	return apierrors.NewServiceUnavailable(fmt.Sprintf("%s: %v", message, err))
}

type upstreamError struct {
	resp *http.Response
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream error: %s", e.resp.Status)
}

func forwardUpstreamResponse(w http.ResponseWriter, resp *http.Response) error {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	if resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
		if _, err := io.Copy(w, resp.Body); err != nil {
			return fmt.Errorf("writing upstream response body: %w", err)
		}
	}
	return nil
}
