package trustadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// ControlClient publishes records through the guest API server's HTTPS service
// proxy. The API server authenticates the publisher; the admission service
// independently verifies the forwarded publisher token with TokenReview.
type ControlClient struct {
	client   *http.Client
	endpoint string
	token    string
}

func NewControlClient(config *rest.Config, namespace string) (*ControlClient, error) {
	if config == nil || config.BearerToken == "" || namespace == "" {
		return nil, fmt.Errorf("publisher connection is not configured")
	}
	base, err := url.Parse(config.Host)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Path != "" && base.Path != "/") {
		return nil, fmt.Errorf("publisher API endpoint is invalid")
	}
	transport, err := rest.TransportFor(config)
	if err != nil {
		return nil, fmt.Errorf("create publisher transport: %w", err)
	}
	base.Path = fmt.Sprintf("/api/v1/namespaces/%s/services/https:%s:https/proxy%s",
		url.PathEscape(namespace), ControlServiceName, ControlPath)
	return &ControlClient{
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: base.String(), token: config.BearerToken,
	}, nil
}

func (c *ControlClient) Publish(ctx context.Context, record ExpectedBundle) error {
	return c.request(ctx, http.MethodPost, ControlPublishRequest{Record: record})
}

func (c *ControlClient) Revoke(ctx context.Context, key RecordKey) error {
	return c.request(ctx, http.MethodDelete, ControlRevokeRequest{Key: key})
}

func (c *ControlClient) request(ctx context.Context, method string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode trust control request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create trust control request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ControlTokenHeader, c.token)
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("send trust control request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("trust control %s failed with HTTP %d", strings.ToLower(method), response.StatusCode)
	}
	return nil
}
