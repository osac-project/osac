/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package bcmclient provides a Go HTTP client for the NVIDIA Base Command
// Manager (BCM) JSON API. It handles mTLS authentication, automatic
// certificate rotation, version validation, and typed error classification.
package bcmclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	jsonPath    = "/json"
	versionPath = "/rest/v1/version"

	minMajorVersion  = 10
	minMinorVersion  = 25
	minPatchVersion  = 3
	defaultTimeoutS  = 30
	maxResponseBytes = 64 << 20 // 64 MiB
)

// ExtraValues keys used by OSAC in BCM device extra_values.
const (
	ExtraValueInstanceID     = "osac_instance_id"
	ExtraValueResourceClass  = "resource_class"
	ExtraValueBMCAddress     = "osac_bmc_address"
	ExtraValueBMCCredentials = "osac_bmc_credentials_secret"
	ExtraValueInterfaceMACs  = "osac_interface_macs"
)

// Typed errors for BCM API failures.
var (
	ErrConnectionFailed = errors.New("bcm connection failed")
	ErrTLSFailed        = errors.New("bcm TLS handshake failed")
	ErrAuthFailed       = errors.New("bcm authentication failed")
	ErrServerError      = errors.New("bcm server error")
	ErrVersionTooOld    = errors.New("bcm version below minimum required")
)

// ValidationError wraps BCM field validation failures from UpdateDevice.
type ValidationError struct {
	Validations []Validation
}

func (e *ValidationError) Error() string {
	msgs := make([]string, 0, len(e.Validations))
	for _, v := range e.Validations {
		msgs = append(msgs, fmt.Sprintf("%s: %s (%s)", v.Field, v.Message, v.ErrorCode))
	}
	return fmt.Sprintf("bcm validation error: %s", strings.Join(msgs, "; "))
}

// Prometheus metrics.
var (
	apiRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_bcm_api_requests_total",
			Help: "Total BCM API calls",
		},
		[]string{"method", "status"},
	)
	apiLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "osac_bcm_api_duration_seconds",
			Help: "BCM API call latency",
		},
		[]string{"method"},
	)
)

var metricsOnce sync.Once

func registerMetrics() {
	metricsOnce.Do(func() {
		metrics.Registry.MustRegister(apiRequestsTotal, apiLatency)
	})
}

// Config holds BCM connection parameters.
type Config struct {
	URL                string `json:"url"`
	CertFile           string `json:"certFile"`
	KeyFile            string `json:"keyFile"`
	CAFile             string `json:"caFile"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

// Validate checks that required fields are set.
func (c *Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("bcm url is required in config")
	}
	if c.CertFile == "" {
		return fmt.Errorf("bcm certFile is required in config")
	}
	if c.KeyFile == "" {
		return fmt.Errorf("bcm keyFile is required in config")
	}
	return nil
}

// Client talks to the BCM JSON API over mTLS.
type Client struct {
	httpClient  *http.Client
	certWatcher *certwatcher.CertWatcher
	baseURL     string
}

// NewClient creates a BCM client. It validates connectivity by checking
// the BCM version at startup.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	registerMetrics()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	httpClient, watcher, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}

	if cfg.InsecureSkipVerify {
		log := ctrllog.FromContext(ctx)
		log.Info("WARNING: BCM TLS certificate verification is disabled")
	}

	c := &Client{
		httpClient:  httpClient,
		certWatcher: watcher,
		baseURL:     strings.TrimRight(cfg.URL, "/"),
	}

	if err := c.checkVersion(ctx); err != nil {
		return nil, fmt.Errorf("bcm version check failed: %w", err)
	}

	return c, nil
}

// CertWatcher returns the certificate watcher for registration with the
// controller manager via mgr.Add(). This enables automatic cert rotation.
func (c *Client) CertWatcher() *certwatcher.CertWatcher {
	return c.certWatcher
}

// NewClientForTest creates a Client with an injected http.Client for testing.
func NewClientForTest(httpClient *http.Client, baseURL string) *Client {
	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func newHTTPClient(cfg *Config) (*http.Client, *certwatcher.CertWatcher, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read bcm CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, nil, fmt.Errorf("failed to parse bcm CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	watcher, err := certwatcher.New(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize bcm certificate watcher: %w", err)
	}
	tlsConfig.GetClientCertificate = func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return watcher.GetCertificate(nil)
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: defaultTimeoutS * time.Second,
	}, watcher, nil
}

func (c *Client) checkVersion(ctx context.Context) error {
	log := ctrllog.FromContext(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+versionPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create version request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: version endpoint returned %d", ErrServerError, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read version response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return fmt.Errorf("bcm version response exceeds %d bytes", maxResponseBytes)
	}

	var version versionResponse
	if err := json.Unmarshal(body, &version); err != nil {
		return fmt.Errorf("failed to parse version response: %w", err)
	}

	var major, minor, patch int
	n, _ := fmt.Sscanf(version.CMVersion, "%d.%d.%d", &major, &minor, &patch)
	if n < 2 {
		return fmt.Errorf("failed to parse cm_version %q: expected at least major.minor", version.CMVersion)
	}
	if major < minMajorVersion ||
		(major == minMajorVersion && minor < minMinorVersion) ||
		(major == minMajorVersion && minor == minMinorVersion && patch < minPatchVersion) {
		return fmt.Errorf("%w: got %s, need >= %d.%d.%d",
			ErrVersionTooOld, version.CMVersion, minMajorVersion, minMinorVersion, minPatchVersion)
	}

	log.Info("BCM version check passed", "cm_version", version.CMVersion, "cmd_version", version.CMDVersion)
	return nil
}

// doJSONCall executes a BCM JSON API call and returns the raw response body.
func (c *Client) doJSONCall(ctx context.Context, service, call string, args any) (json.RawMessage, error) { //nolint:gocyclo
	log := ctrllog.FromContext(ctx)

	if args == nil {
		args = []any{}
	}

	reqBody := jsonRequest{
		Service: service,
		Call:    call,
		Args:    args,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bcm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+jsonPath, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create bcm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start).Seconds()
	apiLatency.WithLabelValues(call).Observe(duration)

	if err != nil {
		apiRequestsTotal.WithLabelValues(call, "error").Inc()
		log.V(1).Info("BCM API call failed", "service", service, "call", call, "error", err)
		return nil, classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	log.V(1).Info("BCM API call completed", "service", service, "call", call,
		"status", resp.StatusCode, "duration_seconds", duration)

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		apiRequestsTotal.WithLabelValues(call, "error").Inc()
		return nil, fmt.Errorf("failed to read bcm response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		apiRequestsTotal.WithLabelValues(call, "error").Inc()
		return nil, fmt.Errorf("bcm response exceeds %d bytes", maxResponseBytes)
	}

	if resp.StatusCode >= 400 {
		apiRequestsTotal.WithLabelValues(call, "error").Inc()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: HTTP %d", ErrAuthFailed, resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: %d %s", ErrServerError, resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("bcm api error: HTTP %d %s", resp.StatusCode, string(body))
	}

	var errResp errorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.ErrorMessage != "" {
		apiRequestsTotal.WithLabelValues(call, "error").Inc()
		msg := strings.TrimSpace(errResp.ErrorMessage)
		if strings.Contains(msg, "certificate") || strings.Contains(msg, "does not allow access") {
			return nil, fmt.Errorf("%w: %s", ErrAuthFailed, msg)
		}
		return nil, fmt.Errorf("bcm api error: %s", msg)
	}

	apiRequestsTotal.WithLabelValues(call, "success").Inc()
	return json.RawMessage(body), nil
}

// GetDevices returns all devices from BCM.
func (c *Client) GetDevices(ctx context.Context) ([]Device, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "getDevices", []any{})
	if err != nil {
		return nil, fmt.Errorf("GetDevices: %w", err)
	}

	var rawDevices []json.RawMessage
	if err := json.Unmarshal(body, &rawDevices); err != nil {
		return nil, fmt.Errorf("GetDevices: failed to parse response: %w", err)
	}

	devices := make([]Device, 0, len(rawDevices))
	for _, raw := range rawDevices {
		var d Device
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("GetDevices: failed to parse device: %w", err)
		}
		d.Raw = raw
		devices = append(devices, d)
	}

	return devices, nil
}

// GetDevice returns a single device by hostname, or nil if not found.
// Always use GetDevice instead of getNode — getNode returns null for LiteNodes.
func (c *Client) GetDevice(ctx context.Context, hostname string) (*Device, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "getDevice", []any{hostname})
	if err != nil {
		return nil, fmt.Errorf("GetDevice %s: %w", hostname, err)
	}

	if string(body) == "null" {
		return nil, nil
	}

	var d Device
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("GetDevice %s: failed to parse response: %w", hostname, err)
	}
	d.Raw = body

	return &d, nil
}

// GetCategories returns all node categories from BCM. Used to resolve
// category-level BMC credentials that a device inherits.
func (c *Client) GetCategories(ctx context.Context) ([]Category, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "getCategories", []any{})
	if err != nil {
		return nil, fmt.Errorf("GetCategories: %w", err)
	}

	var categories []Category
	if err := json.Unmarshal(body, &categories); err != nil {
		return nil, fmt.Errorf("GetCategories: failed to parse response: %w", err)
	}
	return categories, nil
}

// GetPartitions returns all partitions from BCM. Used to resolve
// partition-level BMC credentials that a device inherits.
func (c *Client) GetPartitions(ctx context.Context) ([]Partition, error) {
	body, err := c.doJSONCall(ctx, "cmpart", "getPartitions", []any{})
	if err != nil {
		return nil, fmt.Errorf("GetPartitions: %w", err)
	}

	var partitions []Partition
	if err := json.Unmarshal(body, &partitions); err != nil {
		return nil, fmt.Errorf("GetPartitions: failed to parse response: %w", err)
	}
	return partitions, nil
}

// UpdateDevice sends a full device object to BCM. The raw JSON must be the
// complete device as returned by GetDevice, with modifications applied.
func (c *Client) UpdateDevice(ctx context.Context, deviceRaw json.RawMessage) (*UpdateResponse, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "updateDevice", []json.RawMessage{deviceRaw})
	if err != nil {
		return nil, fmt.Errorf("UpdateDevice: %w", err)
	}

	var resp UpdateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("UpdateDevice: failed to parse response: %w", err)
	}

	if !resp.Success {
		if len(resp.Validation) > 0 {
			return &resp, &ValidationError{Validations: resp.Validation}
		}
		return &resp, fmt.Errorf("UpdateDevice: bcm reported failure without validation details")
	}

	return &resp, nil
}

// SetExtraValue updates a single key in the device's extra_values and returns
// the modified raw JSON for use with UpdateDevice.
func SetExtraValue(deviceRaw json.RawMessage, key string, value any) (json.RawMessage, error) {
	obj, err := unmarshalPreservingNumbers(deviceRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal device for extra_values update: %w", err)
	}

	extraValues, _ := obj["extra_values"].(map[string]any)
	if extraValues == nil {
		extraValues = make(map[string]any)
	}
	extraValues[key] = value
	obj["extra_values"] = extraValues

	return json.Marshal(obj)
}

// RemoveExtraValue removes a key from the device's extra_values and returns
// the modified raw JSON for use with UpdateDevice.
func RemoveExtraValue(deviceRaw json.RawMessage, key string) (json.RawMessage, error) {
	obj, err := unmarshalPreservingNumbers(deviceRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal device for extra_values removal: %w", err)
	}

	if extraValues, ok := obj["extra_values"].(map[string]any); ok {
		delete(extraValues, key)
		obj["extra_values"] = extraValues
	}

	return json.Marshal(obj)
}

func unmarshalPreservingNumbers(deviceRaw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(deviceRaw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func classifyHTTPError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return fmt.Errorf("%w: %v", ErrTLSFailed, err)
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "tls:") || strings.Contains(errMsg, "certificate") ||
		strings.Contains(errMsg, "x509:") {
		return fmt.Errorf("%w: %v", ErrTLSFailed, err)
	}

	return fmt.Errorf("%w: %v", ErrConnectionFailed, err)
}
