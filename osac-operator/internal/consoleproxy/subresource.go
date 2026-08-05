package consoleproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

const upstreamDialTimeout = 30 * time.Second

func (s *Server) handleSubresource(w http.ResponseWriter, r *http.Request, subresource string) {
	ctx := r.Context()
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	vmClusterConfig, configSource, err := s.configResolver.ResolveConfig(ctx, namespace)
	if err != nil {
		s.writeFailure(ctx, w, r, "Failed to resolve VM cluster config",
			newResolveConfigStatusError(err),
			slog.String("namespace", namespace),
		)
		return
	}

	s.logger.InfoContext(ctx, "Subresource request",
		slog.String("subresource", subresource),
		slog.String("namespace", namespace),
		slog.String("name", name),
		slog.String("configSource", configSource),
	)

	vmNamespace, vmName, err := s.resolveVMReference(ctx, namespace, name)
	if err != nil {
		s.writeFailure(ctx, w, r, "Failed to resolve VM reference",
			err,
			slog.String("name", name),
		)
		return
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, upstreamDialTimeout)
	defer dialCancel()

	upstreamWS, err := s.dialSubresource(dialCtx, vmClusterConfig, vmNamespace, vmName, subresource)
	if err != nil {
		var ue *upstreamError
		if errors.As(err, &ue) {
			s.logger.ErrorContext(ctx, "Upstream error",
				slog.String("subresource", subresource),
				slog.String("vm", vmName),
				slog.String("status", ue.resp.Status),
				slog.String("configSource", configSource),
			)
			if err := forwardUpstreamResponse(w, ue.resp); err != nil {
				s.logger.ErrorContext(ctx, "Failed to forward upstream response",
					slog.String("error", err.Error()),
				)
			}
			return
		}
		s.writeFailure(ctx, w, r, "Failed to connect to VM "+subresource,
			err,
			slog.String("vm", vmName),
			slog.String("configSource", configSource),
		)
		return
	}
	defer func() { _ = upstreamWS.CloseNow() }()

	// Skip origin check: K8s API aggregation sets Host to the backend ClusterIP
	// while Origin retains the client-facing API server address.
	// Auth is handled by delegated request-header authentication and SubjectAccessReview/RBAC.
	clientWS, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to accept WebSocket connection",
			slog.String("error", err.Error()),
		)
		return
	}
	defer func() { _ = clientWS.CloseNow() }()

	proxyCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	upstreamConn := websocket.NetConn(proxyCtx, upstreamWS, websocket.MessageBinary)
	clientConn := websocket.NetConn(proxyCtx, clientWS, websocket.MessageBinary)

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(upstreamConn, clientConn)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(clientConn, upstreamConn)
		errCh <- err
	}()

	err = <-errCh
	cancel()

	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		s.logger.WarnContext(ctx, "Stream error",
			slog.String("subresource", subresource),
			slog.String("namespace", namespace),
			slog.String("name", name),
			slog.String("error", err.Error()),
		)
	}

	s.logger.InfoContext(ctx, "Session ended",
		slog.String("subresource", subresource),
		slog.String("namespace", namespace),
		slog.String("name", name),
	)
}

func (s *Server) resolveVMReference(ctx context.Context, namespace, name string) (vmNamespace, vmName string, err error) {
	ci := &osacv1alpha1.ComputeInstance{}
	err = s.hubClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, ci)
	if err != nil {
		return "", "", newComputeInstanceLookupStatusError(name, err)
	}

	ref := ci.Status.VirtualMachineReference
	if ref == nil {
		return "", "", newVMReferenceStatusError(name, "virtual machine reference is not available yet")
	}
	if ref.Namespace == "" || ref.KubeVirtVirtualMachineName == "" {
		return "", "", newVMReferenceStatusError(name, "virtual machine reference is incomplete")
	}
	return ref.Namespace, ref.KubeVirtVirtualMachineName, nil
}

func (s *Server) dialSubresource(ctx context.Context, remoteConfig *rest.Config, namespace, vmName, subresource string) (*websocket.Conn, error) {
	subresourceURL, err := kubevirtSubresourceURL(remoteConfig, namespace, vmName, subresource)
	if err != nil {
		return nil, newConfigStatusError("failed to build VM subresource URL", err)
	}

	rt, err := rest.TransportFor(remoteConfig)
	if err != nil {
		return nil, newConfigStatusError("failed to create upstream transport", err)
	}

	conn, resp, err := websocket.Dial(ctx, subresourceURL.String(), &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		return nil, newConnectStatusError(err, resp)
	}
	return conn, nil
}

func (s *Server) writeFailure(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	message string,
	err error,
	attrs ...any,
) {
	logAttrs := append(attrs, slog.String("error", err.Error()))
	s.logger.ErrorContext(ctx, message, logAttrs...)
	writeError(w, r, err)
}

func kubevirtSubresourceURL(remoteConfig *rest.Config, namespace, vmName, subresource string) (*url.URL, error) {
	u, err := url.Parse(remoteConfig.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote host: %w", err)
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported remote host protocol %q", u.Scheme)
	}

	u = u.JoinPath(fmt.Sprintf(
		"apis/subresources.kubevirt.io/%s/namespaces/%s/virtualmachineinstances/%s/%s",
		kubevirtv1.ApiStorageVersion,
		namespace,
		vmName,
		subresource,
	))
	return u, nil
}
