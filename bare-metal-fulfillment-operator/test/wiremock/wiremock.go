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

// Package wiremock starts and connects to shared WireMock test instances.
package wiremock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	_ "github.com/testcontainers/testcontainers-go/modules/podman" // Register the Podman provider.
	"github.com/testcontainers/testcontainers-go/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	image    = "localhost/osac-wiremock:it"
	httpPort = "8080/tcp"
)

// Start runs an isolated WireMock instance on a dynamically assigned loopback
// port. Call stop to remove it when the spec finishes.
func Start(ctx context.Context) (string, func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	instance, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts(httpPort),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			config.PortBindings = network.PortMap{
				network.MustParsePort(httpPort): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/__admin/health").WithPort(httpPort)),
	)
	// Startup can fail after the container has been created.
	stop := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return instance.Terminate(cleanupCtx, testcontainers.StopTimeout(time.Second))
	}
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("start WireMock (build with make wiremock-image): %w", err), stop())
	}

	baseURL, err := instance.PortEndpoint(ctx, httpPort, "http")
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("discover WireMock endpoint: %w", err), stop())
	}
	return baseURL, stop, nil
}

// Admin sends fixture setup and inspection requests to WireMock. Kind supplies
// an adapter through the Kubernetes Service proxy.
type Admin func(ctx context.Context, method, path string, body []byte) ([]byte, error)

// HTTPAdmin connects to a locally running WireMock instance.
func HTTPAdmin(baseURL string) Admin {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context, method, path string, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("WireMock %s %s returned HTTP %d", method, path, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
}

// ServiceProxyAdmin accesses WireMock through the Kubernetes API server. Test
// setup can use the admin API without opening a host port for the fixture.
func ServiceProxyAdmin(cluster kubernetes.Interface, namespace, service, port string) Admin {
	return func(ctx context.Context, method, path string, body []byte) ([]byte, error) {
		request := cluster.CoreV1().RESTClient().Verb(method).
			Namespace(namespace).Resource("services").Name("http:" + service + ":" + port).
			SubResource("proxy").Suffix(strings.TrimPrefix(path, "/"))
		if body != nil {
			request.SetHeader("Content-Type", "application/json").Body(body)
		}
		return request.DoRaw(ctx)
	}
}
