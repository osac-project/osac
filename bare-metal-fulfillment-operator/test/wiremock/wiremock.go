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
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
)

//go:embed image.txt
var wireMockImage string

// Start runs an isolated WireMock instance on a dynamically assigned loopback
// port. Call stop to remove it when the spec finishes.
func Start(ctx context.Context) (baseURL string, stop func() error, err error) {
	tool := os.Getenv("CONTAINER_TOOL")
	if tool == "" {
		tool = "docker"
		if _, err := exec.LookPath(tool); err != nil {
			tool = "podman"
		}
	}

	image := strings.TrimSpace(wireMockImage)

	startCtx, cancelStart := context.WithTimeout(ctx, 3*time.Minute)
	output, err := exec.CommandContext(startCtx, tool,
		"run", "--detach", "--rm", "--pull=missing", "--publish", "127.0.0.1::8080", image,
	).Output()
	cancelStart()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil, fmt.Errorf("start WireMock image %s: %w: %s", image, err, exitErr.Stderr)
		}
		return "", nil, fmt.Errorf("start WireMock: %w", err)
	}

	id := strings.TrimSpace(string(output))
	stop = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, tool, "stop", "--time", "1", id).CombinedOutput()
		if err != nil {
			return fmt.Errorf("stop WireMock: %w: %s", err, output)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err = exec.CommandContext(ctx, tool, "port", id, "8080/tcp").CombinedOutput()
	if err != nil {
		_ = stop()
		return "", nil, fmt.Errorf("discover WireMock port: %w: %s", err, output)
	}
	baseURL = "http://" + strings.TrimSpace(string(output))
	admin := HTTPAdmin(baseURL)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := admin(ctx, http.MethodGet, "/__admin/health", nil); err == nil {
			return baseURL, stop, nil
		}
		select {
		case <-ctx.Done():
			_ = stop()
			return "", nil, fmt.Errorf("wait for WireMock readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
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
