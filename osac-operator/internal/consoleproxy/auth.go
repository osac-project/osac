package consoleproxy

import (
	"context"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/apiserver/pkg/endpoints/filters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// authController combines the two ConfigMap watchers for the front-proxy CA
// and request header configuration. Lightweight replacement for
// k8s.io/apiserver/pkg/server/options.DynamicRequestHeaderController.
type authController struct {
	ca      *dynamiccertificates.ConfigMapCAController
	headers *headerrequest.RequestHeaderAuthRequestController
}

func (c *authController) RunOnce(ctx context.Context) error {
	if err := c.ca.RunOnce(ctx); err != nil {
		return err
	}
	return c.headers.RunOnce(ctx)
}

func (c *authController) Run(ctx context.Context) {
	go c.ca.Run(ctx, 1)
	go c.headers.Run(ctx, 1)
}

func buildAuthHandler(ctx context.Context, handler http.Handler, hubConfig *rest.Config) (http.Handler, *authController, error) {
	kubeClient, err := clientset.NewForConfig(hubConfig)
	if err != nil {
		return nil, nil, err
	}

	caProvider, err := dynamiccertificates.NewDynamicCAFromConfigMapController(
		"client-ca",
		"kube-system",
		"extension-apiserver-authentication",
		"requestheader-client-ca-file",
		kubeClient,
	)
	if err != nil {
		return nil, nil, err
	}

	headerProvider := headerrequest.NewRequestHeaderAuthRequestController(
		"extension-apiserver-authentication",
		"kube-system",
		kubeClient,
		"requestheader-username-headers",
		"requestheader-uid-headers",
		"requestheader-group-headers",
		"requestheader-extra-headers-prefix",
		"requestheader-allowed-names",
	)

	ctrl := &authController{ca: caProvider, headers: headerProvider}
	if err := ctrl.RunOnce(ctx); err != nil {
		return nil, nil, err
	}

	requestHeaderConfig := &authenticatorfactory.RequestHeaderConfig{
		CAContentProvider:   caProvider,
		UsernameHeaders:     headerrequest.StringSliceProviderFunc(headerProvider.UsernameHeaders),
		UIDHeaders:          headerrequest.StringSliceProviderFunc(headerProvider.UIDHeaders),
		GroupHeaders:        headerrequest.StringSliceProviderFunc(headerProvider.GroupHeaders),
		ExtraHeaderPrefixes: headerrequest.StringSliceProviderFunc(headerProvider.ExtraHeaderPrefixes),
		AllowedClientNames:  headerrequest.StringSliceProviderFunc(headerProvider.AllowedClientNames),
	}

	authnConfig := authenticatorfactory.DelegatingAuthenticatorConfig{
		RequestHeaderConfig: requestHeaderConfig,
	}
	authn, _, err := authnConfig.New()
	if err != nil {
		return nil, nil, err
	}

	authzConfig := authorizerfactory.DelegatingAuthorizerConfig{
		SubjectAccessReviewClient: kubeClient.AuthorizationV1(),
		AllowCacheTTL:             5 * time.Minute,
		DenyCacheTTL:              30 * time.Second,
		WebhookRetryBackoff: &wait.Backoff{
			Duration: 500 * time.Millisecond,
			Factor:   1.5,
			Jitter:   0.2,
			Steps:    5,
		},
	}
	authz, err := authzConfig.New()
	if err != nil {
		return nil, nil, err
	}

	h := wrapWithAuthFilters(handler, authn, authz, requestHeaderConfig)
	return h, ctrl, nil
}

func wrapWithAuthFilters(
	handler http.Handler,
	authn authenticator.Request,
	authz authorizer.Authorizer,
	requestHeaderConfig *authenticatorfactory.RequestHeaderConfig,
) http.Handler {
	requestInfoResolver := &request.RequestInfoFactory{
		APIPrefixes:          sets.NewString("apis"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}

	h := filters.WithAuthorization(handler, authz, scheme.Codecs)
	h = filters.WithAuthentication(h, authn,
		filters.Unauthorized(scheme.Codecs),
		nil,
		requestHeaderConfig,
	)
	h = filters.WithRequestInfo(h, requestInfoResolver)
	return h
}
