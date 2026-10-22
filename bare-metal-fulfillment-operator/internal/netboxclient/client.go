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

// Package netboxclient provides the BMF operator's narrow, secure NetBox API
// boundary. Device routes and models come from the generated go-netbox SDK.
package netboxclient

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	netbox "github.com/netbox-community/go-netbox/v4"
)

const (
	defaultPageSize    = int32(100)
	maxGETAttempts     = 3
	initialBackoff     = 100 * time.Millisecond
	defaultHTTPTimeout = 30 * time.Second
	// Bound a complete page walk; the HTTP timeout only bounds one request.
	maxPaginationDuration = 5 * time.Minute
	maxInt32Offset        = int64(1<<31 - 1)
)

// DeviceQuery accepts the generated SDK's status filter and exact custom-field
// query keys in NetBox's cf_<field> form.
type DeviceQuery map[string]string

// Device contains the fields consumed by the BMF inventory adapter.
type Device struct {
	ID           string
	Name         string
	Status       string
	CustomFields map[string]interface{}
}

// CustomField describes a NetBox custom field attached to dcim.device.
type CustomField struct {
	Name        string
	Label       string
	Type        string
	DataType    string
	Required    bool
	ObjectTypes []string
}

// DeviceSnapshot pairs a device with the ETag from its detail response.
type DeviceSnapshot struct {
	Device Device
	ETag   string
}

// DevicePatch limits writes to device status, custom fields, and an optional
// already-sanitized NetBox changelog message.
type DevicePatch struct {
	Status           *string
	CustomFields     map[string]interface{}
	ChangelogMessage string
}

// Client wraps generated NetBox operations with safe transport, retry, and
// diagnostic behavior.
type Client struct {
	api *netbox.APIClient
}

// New validates configuration and creates a client without making a network
// request. Connectivity and schema checks remain the responsibility of callers.
func New(config Config) (*Client, error) {
	endpoint, err := normalizeEndpoint(config.Endpoint, config.AllowInsecureHTTP)
	if err != nil {
		return nil, err
	}
	token, err := readToken(config.TokenFile)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := newTLSConfig(config.CAFile)
	if err != nil {
		return nil, err
	}

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unsupported default HTTP transport for NetBox")
	}
	transport := defaultTransport.Clone()
	transport.TLSClientConfig = tlsConfig

	httpClient := &http.Client{
		Transport: &requestExtensionTransport{base: transport},
		Timeout:   defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	configuration := netbox.NewConfiguration()
	configuration.Servers[0].URL = endpoint
	configuration.Debug = false
	configuration.AddDefaultHeader("Authorization", "Token "+token)
	configuration.HTTPClient = httpClient

	return &Client{api: netbox.NewAPIClient(configuration)}, nil
}

// ForEachDevice visits matching devices page by page. The visitor returns true
// to continue or false to stop successfully. Only idempotent GET requests are
// retried.
func (c *Client) ForEachDevice(ctx context.Context, query DeviceQuery, visit func(Device) (bool, error)) error {
	if visit == nil {
		return &APIError{Kind: ErrorKindInvalidRequest}
	}
	status, customFields, err := splitDeviceQuery(query)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(nonNilContext(ctx), maxPaginationDuration)
	defer cancel()

	var offset int32
	for {
		currentOffset := offset
		page, _, err := c.listDevicePage(ctx, status, customFields, currentOffset)
		if err != nil {
			return err
		}
		if page == nil {
			return &APIError{Kind: ErrorKindInvalidResponse}
		}

		hasNext := page.GetNext() != ""
		nextOffset, err := nextPaginationOffset(offset, page.Count, len(page.Results), hasNext)
		if err != nil {
			return err
		}
		for _, device := range page.Results {
			if err := ctx.Err(); err != nil {
				return err
			}
			continueListing, err := visit(fromSDKDevice(device))
			if err != nil {
				return err
			}
			if !continueListing {
				return ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !hasNext {
			return nil
		}
		offset = nextOffset
	}
}

// listDevicePage retrieves one device page using the shared GET retry policy.
func (c *Client) listDevicePage(
	ctx context.Context,
	status string,
	customFields map[string]string,
	offset int32,
) (*netbox.PaginatedDeviceWithConfigContextList, *http.Response, error) {
	return retryGET(ctx, func(requestContext context.Context) (*netbox.PaginatedDeviceWithConfigContextList, *http.Response, error) {
		if len(customFields) > 0 {
			requestContext = withRequestOptions(requestContext, requestOptions{
				customFieldQuery: customFields,
			})
		}
		request := c.api.DcimAPI.DcimDevicesList(requestContext).
			Limit(defaultPageSize).
			Offset(offset)
		if status != "" {
			request = request.Status([]string{status})
		}
		return request.Execute()
	})
}

// ForEachDeviceCustomField visits custom-field definitions attached to
// dcim.device. The visitor returns true to continue or false to stop
// successfully.
func (c *Client) ForEachDeviceCustomField(ctx context.Context, visit func(CustomField) (bool, error)) error {
	if visit == nil {
		return &APIError{Kind: ErrorKindInvalidRequest}
	}

	ctx, cancel := context.WithTimeout(nonNilContext(ctx), maxPaginationDuration)
	defer cancel()

	var offset int32
	for {
		currentOffset := offset
		page, _, err := retryGET(ctx, func(requestContext context.Context) (*netbox.PaginatedCustomFieldList, *http.Response, error) {
			return c.api.ExtrasAPI.ExtrasCustomFieldsList(requestContext).
				ObjectType("dcim.device").
				Limit(defaultPageSize).
				Offset(currentOffset).
				Execute()
		})
		if err != nil {
			return err
		}
		if page == nil {
			return &APIError{Kind: ErrorKindInvalidResponse}
		}

		hasNext := page.GetNext() != ""
		nextOffset, err := nextPaginationOffset(offset, page.Count, len(page.Results), hasNext)
		if err != nil {
			return err
		}
		for _, field := range page.Results {
			if err := ctx.Err(); err != nil {
				return err
			}
			continueListing, err := visit(CustomField{
				Name:        field.Name,
				Label:       field.GetLabel(),
				Type:        string(field.Type.GetValue()),
				DataType:    field.DataType,
				Required:    field.GetRequired(),
				ObjectTypes: append([]string(nil), field.ObjectTypes...),
			})
			if err != nil {
				return err
			}
			if !continueListing {
				return ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !hasNext {
			return nil
		}
		offset = nextOffset
	}
}

// nextPaginationOffset rejects page metadata that could make pagination stop
// too late, overflow the SDK's int32 offset, or continue after the reported
// result count has already been consumed.
func nextPaginationOffset(offset, total int32, resultCount int, hasNext bool) (int32, error) {
	if offset < 0 || total < 0 || resultCount < 0 || resultCount > int(defaultPageSize) {
		return 0, &APIError{Kind: ErrorKindInvalidResponse}
	}

	nextOffset := int64(offset) + int64(resultCount)
	if nextOffset > maxInt32Offset {
		return 0, &APIError{Kind: ErrorKindInvalidResponse}
	}
	if hasNext && (resultCount == 0 || nextOffset >= int64(total)) {
		return 0, &APIError{Kind: ErrorKindInvalidResponse}
	}

	return int32(nextOffset), nil
}

// GetDevice retrieves a device by its stable numeric NetBox ID and returns
// the response ETag unchanged.
func (c *Client) GetDevice(ctx context.Context, id string) (DeviceSnapshot, error) {
	numericID, err := parseDeviceID(id)
	if err != nil {
		return DeviceSnapshot{}, err
	}

	device, response, err := retryGET(nonNilContext(ctx), func(requestContext context.Context) (*netbox.DeviceWithConfigContext, *http.Response, error) {
		return c.api.DcimAPI.DcimDevicesRetrieve(requestContext, numericID).Execute()
	})
	if err != nil {
		return DeviceSnapshot{}, err
	}
	if device == nil || response == nil {
		return DeviceSnapshot{}, &APIError{Kind: ErrorKindInvalidResponse}
	}

	return DeviceSnapshot{
		Device: fromSDKDevice(*device),
		ETag:   response.Header.Get("ETag"),
	}, nil
}

// PatchDevice sends one conditional PATCH. A missing or invalid ETag fails
// locally; writes are never automatically retried after an uncertain outcome.
func (c *Client) PatchDevice(ctx context.Context, id string, patch DevicePatch, etag string) (DeviceSnapshot, error) {
	numericID, err := parseDeviceID(id)
	if err != nil {
		return DeviceSnapshot{}, err
	}
	if strings.TrimSpace(etag) == "" || strings.ContainsAny(etag, "\r\n") {
		return DeviceSnapshot{}, &APIError{Kind: ErrorKindInvalidRequest}
	}
	body, err := toSDKPatch(patch)
	if err != nil {
		return DeviceSnapshot{}, err
	}

	ctx = withRequestOptions(nonNilContext(ctx), requestOptions{ifMatch: etag})
	device, response, err := c.api.DcimAPI.DcimDevicesPartialUpdate(ctx, numericID).
		PatchedWritableDeviceWithConfigContextRequest(*body).
		Execute()
	if err != nil {
		return DeviceSnapshot{}, classifyError(ctx, response, err)
	}
	if device == nil || response == nil {
		return DeviceSnapshot{}, &APIError{Kind: ErrorKindInvalidResponse}
	}

	return DeviceSnapshot{
		Device: fromSDKDevice(*device),
		ETag:   response.Header.Get("ETag"),
	}, nil
}

func retryGET[T any](ctx context.Context, request func(context.Context) (T, *http.Response, error)) (T, *http.Response, error) {
	var zero T
	for attempt := 0; attempt < maxGETAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, nil, err
		}

		value, response, err := request(ctx)
		if err == nil {
			return value, response, nil
		}
		if !retryableGETFailure(ctx, response) || attempt == maxGETAttempts-1 {
			return zero, response, classifyError(ctx, response, err)
		}
		if err := waitForRetry(ctx, initialBackoff*time.Duration(1<<attempt)); err != nil {
			return zero, response, err
		}
	}
	return zero, nil, &APIError{Kind: ErrorKindTransport}
}

func retryableGETFailure(ctx context.Context, response *http.Response) bool {
	if ctx.Err() != nil {
		return false
	}
	if response == nil {
		return true
	}

	switch response.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return response.StatusCode >= 500
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func splitDeviceQuery(query DeviceQuery) (string, map[string]string, error) {
	var status string
	customFields := make(map[string]string)
	for key, value := range query {
		switch {
		case key == "status":
			if value == "" {
				return "", nil, &APIError{Kind: ErrorKindInvalidRequest}
			}
			status = value
		case validCustomFieldFilter(key):
			customFields[key] = value
		default:
			return "", nil, &APIError{Kind: ErrorKindInvalidRequest}
		}
	}
	return status, customFields, nil
}

func validCustomFieldFilter(key string) bool {
	const prefix = "cf_"
	return strings.HasPrefix(key, prefix) && validCustomFieldName(key[len(prefix):])
}

func parseDeviceID(id string) (int32, error) {
	numericID, err := strconv.ParseInt(id, 10, 32)
	if err != nil || numericID <= 0 {
		return 0, &APIError{Kind: ErrorKindInvalidRequest}
	}
	return int32(numericID), nil
}

func toSDKPatch(patch DevicePatch) (*netbox.PatchedWritableDeviceWithConfigContextRequest, error) {
	request := netbox.NewPatchedWritableDeviceWithConfigContextRequest()
	hasChanges := false

	if patch.Status != nil {
		if *patch.Status == "" {
			return nil, &APIError{Kind: ErrorKindInvalidRequest}
		}
		status := netbox.DeviceStatusValue(*patch.Status)
		request.Status = &status
		hasChanges = true
	}
	if patch.CustomFields != nil {
		for key := range patch.CustomFields {
			if !validCustomFieldName(key) {
				return nil, &APIError{Kind: ErrorKindInvalidRequest}
			}
		}
		if len(patch.CustomFields) > 0 {
			request.CustomFields = cloneCustomFields(patch.CustomFields)
			hasChanges = true
		}
	}
	if patch.ChangelogMessage != "" {
		if request.AdditionalProperties == nil {
			request.AdditionalProperties = make(map[string]interface{})
		}
		request.AdditionalProperties["changelog_message"] = patch.ChangelogMessage
		hasChanges = true
	}
	if !hasChanges {
		return nil, &APIError{Kind: ErrorKindInvalidRequest}
	}
	return request, nil
}

func validCustomFieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func fromSDKDevice(device netbox.DeviceWithConfigContext) Device {
	status := ""
	if device.Status != nil {
		status = string(device.Status.GetValue())
	}
	return Device{
		ID:           strconv.FormatInt(int64(device.Id), 10),
		Name:         device.GetName(),
		Status:       status,
		CustomFields: cloneCustomFields(device.CustomFields),
	}
}

func cloneCustomFields(fields map[string]interface{}) map[string]interface{} {
	if fields == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
