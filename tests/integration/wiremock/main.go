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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	_ "github.com/testcontainers/testcontainers-go/modules/podman" // Register the Podman provider.
	"github.com/testcontainers/testcontainers-go/wait"
)

const httpPort = "8080/tcp"

// main keeps the container alive until its parent closes stdin or sends a signal.
func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: wiremock-launcher IMAGE")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	if err := run(ctx, os.Args[1]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// run reports the ready endpoint and removes the container on shutdown.
func run(ctx context.Context, image string) (err error) {
	startCtx, cancelStart := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelStart()
	instance, err := testcontainers.Run(startCtx, image,
		testcontainers.WithExposedPorts(httpPort),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			config.PortBindings = network.PortMap{
				network.MustParsePort(httpPort): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/__admin/health").WithPort(httpPort).WithStartupTimeout(30*time.Second)),
	)
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		err = errors.Join(err, instance.Terminate(cleanupCtx, testcontainers.StopTimeout(time.Second)))
	}()
	if err != nil {
		return fmt.Errorf("start WireMock image %s: %w", image, err)
	}
	endpoint, err := instance.PortEndpoint(startCtx, httpPort, "http")
	if err != nil {
		return fmt.Errorf("discover WireMock endpoint: %w", err)
	}
	if _, err := fmt.Println(endpoint); err != nil {
		return fmt.Errorf("write WireMock endpoint: %w", err)
	}
	<-ctx.Done()
	return nil
}
