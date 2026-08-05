/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package version

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// InterceptorBuilder contains the data and logic needed to build an interceptor that adds version information to the
// gRPC calls. Don't create instances of this type directly, use the NewInterceptor function instead.
type InterceptorBuilder struct {
	logger  *slog.Logger
	product string
	version string
}

// Interceptor contains the data needed by the interceptor.
type Interceptor struct {
	logger  *slog.Logger
	product string
	version string
}

// NewInterceptor creates a builder that can then be used to configure and create a interceptor.
func NewInterceptor() *InterceptorBuilder {
	return &InterceptorBuilder{}
}

// SetLogger sets the logger that will be used by the intercetor. This is mandatory.
func (b *InterceptorBuilder) SetLogger(value *slog.Logger) *InterceptorBuilder {
	b.logger = value
	return b
}

// SetProduct sets the product name that will be used in the user agent header.
func (b *InterceptorBuilder) SetProduct(value string) *InterceptorBuilder {
	b.product = value
	return b
}

// SetVersion sets the version that will be used in the user agent header.
func (b *InterceptorBuilder) SetVersion(value string) *InterceptorBuilder {
	b.version = value
	return b
}

// defaultProduct calculates the default product name from the binary path.
func (b *InterceptorBuilder) defaultProduct() string {
	executable, err := os.Executable()
	if err != nil {
		if b.logger != nil {
			b.logger.Warn(
				"Failed to get executable path for product name",
				slog.Any("error", err),
			)
		}
		return Unknown
	}
	return filepath.Base(executable)
}

// defaultVersion calculates the default version from version.Get() or git information.
func (b *InterceptorBuilder) defaultVersion() string {
	// First try the injected version:
	version := Get()
	if version != Unknown {
		return version
	}

	// Fall back to git information from build info:
	info, ok := debug.ReadBuildInfo()
	if ok {
		var revision string
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			}
		}
		if revision != "" {
			return revision
		}
	}

	return Unknown
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *InterceptorBuilder) Build() (result *Interceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Use provided values or defaults:
	product := b.product
	if product == "" {
		product = b.defaultProduct()
	}
	version := b.version
	if version == "" {
		version = b.defaultVersion()
	}

	// Create and populate the object:
	result = &Interceptor{
		logger:  b.logger,
		product: product,
		version: version,
	}
	return
}

// userAgent calculates the value for the `User-Agent` header.
func (i *Interceptor) userAgentHeaderValue() string {
	return fmt.Sprintf("%s/%s", i.product, i.version)
}

// UnaryClient is the unary client interceptor function that adds the version details.
func (i *Interceptor) UnaryClient(ctx context.Context, method string, request, response any,
	conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ctx = metadata.AppendToOutgoingContext(ctx, userAgentHeaderName, i.userAgentHeaderValue())
	return invoker(ctx, method, request, response, conn, opts...)
}

// StreamClient is the stream client interceptor function that adds the user agent header.
func (i *Interceptor) StreamClient(ctx context.Context, desc *grpc.StreamDesc, conn *grpc.ClientConn, method string,
	streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, userAgentHeaderName, i.userAgentHeaderValue())
	return streamer(ctx, desc, conn, method, opts...)
}

// userAgentHeaderName is the name of the user agent header.
const userAgentHeaderName = "User-Agent"
