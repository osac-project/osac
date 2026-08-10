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

package inventory

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
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	_ Client        = (*BCMClient)(nil)
	_ NewClientFunc = NewClientFunc(NewBCMClient)
)

const (
	bcmJSONPath    = "/json"
	bcmVersionPath = "/rest/v1/version"

	bcmMinMajorVersion = 10
	bcmMinMinorVersion = 25
	bcmDefaultTimeoutS = 30
)

// BCM extra_values keys used by OSAC
const (
	BCMExtraValueInstanceID     = "osac_instance_id"
	BCMExtraValueResourceClass  = "resource_class"
	BCMExtraValueBMCAddress     = "osac_bmc_address"
	BCMExtraValueBMCCredentials = "osac_bmc_credentials_secret"
)

// Typed errors for BCM API failures
var (
	ErrBCMConnectionFailed = errors.New("bcm connection failed")
	ErrBCMTLSFailed        = errors.New("bcm TLS handshake failed")
	ErrBCMAuthFailed       = errors.New("bcm authentication failed")
	ErrBCMServerError      = errors.New("bcm server error")
	ErrBCMVersionTooOld    = errors.New("bcm version below minimum required")
)

// BCMValidationError wraps BCM field validation failures from updateDevice.
type BCMValidationError struct {
	Validations []bcmValidation
}

func (e *BCMValidationError) Error() string {
	msgs := make([]string, 0, len(e.Validations))
	for _, v := range e.Validations {
		msgs = append(msgs, fmt.Sprintf("%s: %s (%s)", v.Field, v.Message, v.ErrorCode))
	}
	return fmt.Sprintf("bcm validation error: %s", strings.Join(msgs, "; "))
}

// Prometheus metrics
var (
	bcmAPIRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_bcm_api_requests_total",
			Help: "Total BCM API calls",
		},
		[]string{"method", "status"},
	)
	bcmAPILatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "osac_bcm_api_duration_seconds",
			Help: "BCM API call latency",
		},
		[]string{"method"},
	)
)

func init() {
	newClientFuncs["bcm"] = NewBCMClient
}

var metricsOnce sync.Once

func registerBCMMetrics() {
	metricsOnce.Do(func() {
		metrics.Registry.MustRegister(bcmAPIRequestsTotal, bcmAPILatency)
	})
}

// bcmClientConfig holds BCM connection parameters parsed from inventory config.
type bcmClientConfig struct {
	URL                string `json:"url"`
	CertFile           string `json:"certFile"`
	KeyFile            string `json:"keyFile"`
	CAFile             string `json:"caFile"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

// BCMClient talks to the BCM JSON API over mTLS.
type BCMClient struct {
	httpClient *http.Client
	baseURL    string
	hostClass  string
}

// bcmJSONRequest is the envelope for all BCM JSON API calls.
type bcmJSONRequest struct {
	Service string `json:"service"`
	Call    string `json:"call"`
	Args    any    `json:"args"`
}

// bcmDevice represents a BCM device with typed access to OSAC-relevant
// fields plus the raw JSON needed for full-object update round-trips.
type bcmDevice struct {
	BaseType    string         `json:"baseType"`
	ChildType   string         `json:"childType"`
	UUID        string         `json:"uuid"`
	Hostname    string         `json:"hostname"`
	MAC         string         `json:"mac"`
	ExtraValues map[string]any `json:"extra_values"`

	raw json.RawMessage
}

// bcmUpdateResponse is the response from cmdevice.updateDevice.
type bcmUpdateResponse struct {
	Success    bool            `json:"success"`
	TaskUUID   string          `json:"task_uuid"`
	Validation []bcmValidation `json:"validation"`
}

type bcmValidation struct {
	ErrorCode string `json:"error_code"`
	Field     string `json:"field"`
	Message   string `json:"message"`
	Severity  string `json:"severity"`
}

// bcmVersionResponse is the response from GET /rest/v1/version.
type bcmVersionResponse struct {
	CMVersion       string `json:"cm_version"`
	CMDVersion      string `json:"cmd_version"`
	BuildHash       string `json:"build_hash"`
	BuildIndex      int    `json:"build_index"`
	DatabaseVersion int    `json:"database_version"`
}

// bcmErrorResponse captures error messages from the BCM JSON API.
type bcmErrorResponse struct {
	ErrorMessage string `json:"errormessage"`
}

// NewBCMClient creates a BCM inventory client. It validates connectivity
// by checking the BCM version at startup.
func NewBCMClient(ctx context.Context, cfg *Config) (Client, error) {
	if cfg.BMHLifecycleManager == nil {
		return nil, fmt.Errorf("BCM inventory backend requires Metal3 management backend")
	}

	registerBCMMetrics()

	bcmCfg, err := parseBCMConfig(cfg)
	if err != nil {
		return nil, err
	}

	httpClient, err := newBCMHTTPClient(bcmCfg)
	if err != nil {
		return nil, err
	}

	c := &BCMClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(bcmCfg.URL, "/"),
		hostClass:  cfg.HostClass,
	}

	if err := c.checkVersion(ctx); err != nil {
		return nil, fmt.Errorf("bcm version check failed: %w", err)
	}

	return c, nil
}

// NewBCMClientForTest creates a BCMClient with an injected http.Client for testing.
func NewBCMClientForTest(httpClient *http.Client, baseURL, hostClass string) *BCMClient {
	return &BCMClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		hostClass:  hostClass,
	}
}

func parseBCMConfig(cfg *Config) (*bcmClientConfig, error) {
	bcmOpts, ok := cfg.Options["bcm"]
	if !ok {
		return nil, fmt.Errorf("bcm options not found in config")
	}

	optsJSON, err := json.Marshal(bcmOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bcm options: %w", err)
	}

	var bcmCfg bcmClientConfig
	if err := json.Unmarshal(optsJSON, &bcmCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bcm options: %w", err)
	}

	if bcmCfg.URL == "" {
		return nil, fmt.Errorf("bcm url is required in config")
	}
	if bcmCfg.CertFile == "" {
		return nil, fmt.Errorf("bcm certFile is required in config")
	}
	if bcmCfg.KeyFile == "" {
		return nil, fmt.Errorf("bcm keyFile is required in config")
	}

	return &bcmCfg, nil
}

func newBCMHTTPClient(cfg *bcmClientConfig) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load bcm client certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read bcm CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse bcm CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: bcmDefaultTimeoutS * time.Second,
	}, nil
}

// checkVersion validates BCM connectivity and minimum version.
func (c *BCMClient) checkVersion(ctx context.Context) error {
	log := ctrllog.FromContext(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+bcmVersionPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create version request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: version endpoint returned %d", ErrBCMServerError, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read version response: %w", err)
	}

	var version bcmVersionResponse
	if err := json.Unmarshal(body, &version); err != nil {
		return fmt.Errorf("failed to parse version response: %w", err)
	}

	var major, minor int
	if _, err := fmt.Sscanf(version.CMVersion, "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("failed to parse cm_version %q: %w", version.CMVersion, err)
	}
	if major < bcmMinMajorVersion || (major == bcmMinMajorVersion && minor < bcmMinMinorVersion) {
		return fmt.Errorf("%w: got %s, need >= %d.%d",
			ErrBCMVersionTooOld, version.CMVersion, bcmMinMajorVersion, bcmMinMinorVersion)
	}

	log.Info("BCM version check passed", "cm_version", version.CMVersion, "cmd_version", version.CMDVersion)
	return nil
}

// doJSONCall executes a BCM JSON API call and returns the raw response body.
func (c *BCMClient) doJSONCall(ctx context.Context, service, call string, args any) (json.RawMessage, error) {
	if args == nil {
		args = []any{}
	}

	reqBody := bcmJSONRequest{
		Service: service,
		Call:    call,
		Args:    args,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bcm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+bcmJSONPath, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create bcm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start).Seconds()
	bcmAPILatency.WithLabelValues(call).Observe(duration)

	if err != nil {
		bcmAPIRequestsTotal.WithLabelValues(call, "error").Inc()
		return nil, classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		bcmAPIRequestsTotal.WithLabelValues(call, "error").Inc()
		return nil, fmt.Errorf("failed to read bcm response: %w", err)
	}

	if resp.StatusCode >= 400 {
		bcmAPIRequestsTotal.WithLabelValues(call, "error").Inc()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: HTTP %d", ErrBCMAuthFailed, resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: %d %s", ErrBCMServerError, resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("bcm api error: HTTP %d %s", resp.StatusCode, string(body))
	}

	var errResp bcmErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.ErrorMessage != "" {
		bcmAPIRequestsTotal.WithLabelValues(call, "error").Inc()
		msg := strings.TrimSpace(errResp.ErrorMessage)
		if strings.Contains(msg, "certificate") || strings.Contains(msg, "does not allow access") {
			return nil, fmt.Errorf("%w: %s", ErrBCMAuthFailed, msg)
		}
		return nil, fmt.Errorf("bcm api error: %s", msg)
	}

	bcmAPIRequestsTotal.WithLabelValues(call, "success").Inc()
	return json.RawMessage(body), nil
}

// getDevices returns all devices from BCM.
func (c *BCMClient) getDevices(ctx context.Context) ([]bcmDevice, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "getDevices", []any{})
	if err != nil {
		return nil, fmt.Errorf("getDevices: %w", err)
	}

	var rawDevices []json.RawMessage
	if err := json.Unmarshal(body, &rawDevices); err != nil {
		return nil, fmt.Errorf("getDevices: failed to parse response: %w", err)
	}

	devices := make([]bcmDevice, 0, len(rawDevices))
	for _, raw := range rawDevices {
		var d bcmDevice
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("getDevices: failed to parse device: %w", err)
		}
		d.raw = raw
		devices = append(devices, d)
	}

	return devices, nil
}

// getDevice returns a single device by hostname, or nil if not found.
// Always use getDevice instead of getNode — getNode returns null for LiteNodes.
func (c *BCMClient) getDevice(ctx context.Context, hostname string) (*bcmDevice, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "getDevice", []any{hostname})
	if err != nil {
		return nil, fmt.Errorf("getDevice %s: %w", hostname, err)
	}

	if string(body) == "null" {
		return nil, nil
	}

	var d bcmDevice
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("getDevice %s: failed to parse response: %w", hostname, err)
	}
	d.raw = body

	return &d, nil
}

// updateDevice sends a full device object to BCM. The raw JSON must be the
// complete device as returned by getDevice, with modifications applied.
func (c *BCMClient) updateDevice(ctx context.Context, deviceRaw json.RawMessage) (*bcmUpdateResponse, error) {
	body, err := c.doJSONCall(ctx, "cmdevice", "updateDevice", []json.RawMessage{deviceRaw})
	if err != nil {
		return nil, fmt.Errorf("updateDevice: %w", err)
	}

	var resp bcmUpdateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("updateDevice: failed to parse response: %w", err)
	}

	if !resp.Success {
		if len(resp.Validation) > 0 {
			return &resp, &BCMValidationError{Validations: resp.Validation}
		}
		return &resp, fmt.Errorf("updateDevice: bcm reported failure without validation details")
	}

	return &resp, nil
}

func unmarshalDevicePreservingNumbers(deviceRaw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(deviceRaw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// setExtraValue updates a single key in the device's extra_values and returns
// the modified raw JSON for use with updateDevice.
func setExtraValue(deviceRaw json.RawMessage, key string, value any) (json.RawMessage, error) {
	obj, err := unmarshalDevicePreservingNumbers(deviceRaw)
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

// removeExtraValue removes a key from the device's extra_values and returns
// the modified raw JSON for use with updateDevice.
func removeExtraValue(deviceRaw json.RawMessage, key string) (json.RawMessage, error) {
	obj, err := unmarshalDevicePreservingNumbers(deviceRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal device for extra_values removal: %w", err)
	}

	if extraValues, ok := obj["extra_values"].(map[string]any); ok {
		delete(extraValues, key)
		obj["extra_values"] = extraValues
	}

	return json.Marshal(obj)
}

// classifyHTTPError maps transport-level errors to typed BCM errors.
func classifyHTTPError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return fmt.Errorf("%w: %v", ErrBCMTLSFailed, err)
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "tls:") || strings.Contains(errMsg, "certificate") ||
		strings.Contains(errMsg, "x509:") {
		return fmt.Errorf("%w: %v", ErrBCMTLSFailed, err)
	}

	return fmt.Errorf("%w: %v", ErrBCMConnectionFailed, err)
}

// FindFreeHost is implemented in Phase 2 (OSAC-3766).
func (c *BCMClient) FindFreeHost(_ context.Context, _ map[string]string) (*Host, error) {
	return nil, fmt.Errorf("bcm FindFreeHost not yet implemented")
}

// AssignHost is implemented in Phase 2 (OSAC-3767).
func (c *BCMClient) AssignHost(_ context.Context, _ string, _ string, _ map[string]string) (*Host, error) {
	return nil, fmt.Errorf("bcm AssignHost not yet implemented")
}

// UnassignHost is implemented in Phase 2 (OSAC-3771).
func (c *BCMClient) UnassignHost(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("bcm UnassignHost not yet implemented")
}
