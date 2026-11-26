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
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
)

//go:embed image.txt
var wireMockImage string

// Launcher builds and runs the Testcontainers executable for envtest.
type Launcher struct {
	executable func() (string, error)
}

// NewLauncher builds on first use so tests without WireMock skip the build.
// The executable's module keeps Testcontainers out of BMF's module graph.
func NewLauncher(t *testing.T, sourceDir string) *Launcher {
	return &Launcher{executable: sync.OnceValues(func() (string, error) {
		binary := filepath.Join(t.TempDir(), "wiremock-launcher")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
		cmd.Dir = sourceDir
		cmd.Env = append(cmd.Environ(), "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build WireMock launcher: %w: %s", err, output)
		}
		return binary, nil
	})}
}

// Start runs an isolated WireMock instance and returns the spec's cleanup.
func (l *Launcher) Start(ctx context.Context) (string, func() error, error) {
	binary, err := l.executable()
	if err != nil {
		return "", nil, err
	}
	tool := os.Getenv("CONTAINER_TOOL")
	if tool == "" {
		tool = "docker"
		if _, err := exec.LookPath(tool); err != nil {
			tool = "podman"
		}
	}
	cmd := exec.Command(binary, strings.TrimSpace(wireMockImage))
	// The launcher cleans up on stdin EOF. This also keeps Ryuk's image pull
	// out of the Quay-only fixture path.
	cmd.Env = append(cmd.Environ(),
		"TESTCONTAINERS_PROVIDER="+filepath.Base(tool),
		"TESTCONTAINERS_RYUK_DISABLED=true",
	)
	var logs bytes.Buffer
	cmd.Stderr = &logs
	input, err := cmd.StdinPipe()
	if err != nil {
		return "", nil, fmt.Errorf("open WireMock launcher input: %w", err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return "", nil, fmt.Errorf("open WireMock launcher output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start WireMock launcher: %w", err)
	}

	stop := func() error {
		_ = input.Close()
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("WireMock launcher: %w: %s", err, logs.String())
		}
		return nil
	}
	stopStartupWatch := context.AfterFunc(ctx, func() { _ = input.Close() })
	defer stopStartupWatch()
	endpoint, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("read WireMock endpoint: %w", err), stop())
	}
	return strings.TrimSpace(endpoint), stop, nil
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
