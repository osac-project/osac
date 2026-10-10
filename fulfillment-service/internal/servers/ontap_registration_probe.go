/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StorageBackendRegistrationProbe verifies management access without changing the array.
type StorageBackendRegistrationProbe interface {
	Probe(ctx context.Context, endpoint, username, password string) error
}

type ontapRegistrationProbe struct {
	client http.Client
}

// NewOntapRegistrationProbe uses system HTTPS trust when client is nil.
func NewOntapRegistrationProbe(client *http.Client) StorageBackendRegistrationProbe {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &ontapRegistrationProbe{client: copy}
}

func (p *ontapRegistrationProbe) Probe(ctx context.Context, endpoint, username, password string) error {
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil ||
		(base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return status.Error(codes.InvalidArgument, "ONTAP endpoint must be a cluster management HTTPS URL without credentials, path, query or fragment")
	}
	if port := base.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return status.Error(codes.InvalidArgument, "ONTAP endpoint has an invalid port")
		}
	}
	if username == "" || password == "" {
		return status.Error(codes.InvalidArgument, "ONTAP discovery username and password must be non-empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	paths := []string{"/api/cluster", "/api/svm/svms", "/api/network/ip/interfaces",
		"/api/network/fc/interfaces", "/api/storage/qos/policies", "/api/storage/volumes", "/api/storage/luns"}
	for _, path := range paths {
		target := *base
		target.Path = path
		query := url.Values{"fields": {"version"}}
		if path != "/api/cluster" {
			query = url.Values{"fields": {"uuid"}, "max_records": {"1"}, "return_timeout": {"2"}}
		}
		target.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return status.Error(codes.InvalidArgument, "invalid ONTAP discovery request")
		}
		request.SetBasicAuth(username, password)
		request.Header.Set("Accept", "application/json")
		if err := p.read(ctx, request, path); err != nil {
			return err
		}
	}
	return nil
}

func (p *ontapRegistrationProbe) read(ctx context.Context, request *http.Request, path string) error {
	response, err := p.client.Do(request)
	if err != nil {
		return ontapTransportError(ctx, err, path)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := codes.Unavailable
		switch response.StatusCode {
		case http.StatusUnauthorized:
			code = codes.InvalidArgument
		case http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
			code = codes.FailedPrecondition
		}
		return status.Errorf(code, "ONTAP discovery GET %s returned HTTP %d; check discovery credentials and read permissions", path, response.StatusCode)
	}
	const maxResponseBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ontapTransportError(ctx, err, path)
	}
	if len(data) > maxResponseBytes {
		return status.Errorf(codes.FailedPrecondition, "ONTAP discovery GET %s exceeded the response size limit", path)
	}
	var result struct {
		Version *struct {
			Full string `json:"full"`
		} `json:"version"`
		Records    []json.RawMessage `json:"records"`
		NumRecords *int              `json:"num_records"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return status.Errorf(codes.FailedPrecondition, "ONTAP discovery GET %s returned an invalid JSON response", path)
	}
	if path == "/api/cluster" {
		if result.Version == nil || result.Version.Full == "" {
			return status.Error(codes.FailedPrecondition, "ONTAP cluster discovery did not return a version")
		}
	} else if result.NumRecords == nil || *result.NumRecords != len(result.Records) || len(result.Records) > 1 {
		return status.Errorf(codes.FailedPrecondition, "ONTAP discovery GET %s returned an invalid collection response", path)
	}
	return nil
}

func ontapTransportError(ctx context.Context, err error, path string) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return status.Error(codes.Canceled, "ONTAP discovery canceled")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "ONTAP discovery timed out")
	}
	return status.Errorf(codes.Unavailable, "ONTAP discovery GET %s failed; check connectivity, HTTPS certificate trust and endpoint identity", path)
}
