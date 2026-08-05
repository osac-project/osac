/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	experiementalcredentials "google.golang.org/grpc/experimental/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// GrpcClientBuilder contains the data and logic needed to create a gRPC client. Don't create instances of this object
// directly, use the NewGrpcClient function instead.
type GrpcClientBuilder struct {
	logger             *slog.Logger
	flags              *pflag.FlagSet
	network            string
	address            string
	host               string
	plaintext          bool
	insecure           bool
	caPool             *x509.CertPool
	tokenSource        auth.TokenSource
	keepAlive          time.Duration
	keepAliveTimeout   time.Duration
	userAgent          string
	unaryInterceptors  []grpc.UnaryClientInterceptor
	streamInterceptors []grpc.StreamClientInterceptor
	metricsSubsystem   string
	metricsRegisterer  prometheus.Registerer
}

// NewGrpcClient creates a builder that can then used to configure and create a gRPC client.
func NewGrpcClient() *GrpcClientBuilder {
	return &GrpcClientBuilder{}
}

// SetLogger sets the logger that the client will use to send messages to the log. This is mandatory.
func (b *GrpcClientBuilder) SetLogger(value *slog.Logger) *GrpcClientBuilder {
	b.logger = value
	return b
}

// SetFlags sets the command line flags that should be used to configure the client.
//
// The name is used to select the options when there are multiple clients. For example, if it is 'API' then it will only
// take into accounts the flags starting with '--api'.
//
// This is optional.
func (b *GrpcClientBuilder) SetFlags(flags *pflag.FlagSet, name string) *GrpcClientBuilder {
	if flags == nil {
		return b
	}

	var (
		flag string
		err  error
	)
	failure := func() {
		b.logger.Error(
			"Failed to get flag value",
			slog.String("flag", flag),
			slog.Any("error", err),
		)
	}

	// Server network:
	flag = grpcClientFlagName(name, grpcClientServerNetworkFlagSuffix)
	serverNetworkValue, err := flags.GetString(flag)
	if err != nil {
		failure()
	} else {
		b.SetNetwork(serverNetworkValue)
	}

	// Server address:
	flag = grpcClientFlagName(name, grpcClientServerAddrFlagSuffix)
	serverAddrValue, err := flags.GetString(flag)
	if err != nil {
		failure()
	} else {
		b.SetAddress(serverAddrValue)
	}

	// Server plaintext:
	flag = grpcClientFlagName(name, grpcClientServerPlaintextFlagSuffix)
	serverPlaintextValue, err := flags.GetBool(flag)
	if err != nil {
		failure()
	} else {
		b.SetPlaintext(serverPlaintextValue)
	}

	// Server insecure:
	flag = grpcClientFlagName(name, grpcClientServerInsecureFlagSuffix)
	serverInsecureValue, err := flags.GetBool(flag)
	if err != nil {
		failure()
	} else {
		b.SetInsecure(serverInsecureValue)
	}

	// Keep alive:
	flag = grpcClientFlagName(name, grpcClientKeepAliveFlagSuffix)
	keepAliveValue, err := flags.GetDuration(flag)
	if err != nil {
		failure()
	} else {
		b.SetKeepAlive(keepAliveValue)
	}

	// Save the flags:
	b.flags = flags

	return b
}

// SetNetwork sets the server network.
func (b *GrpcClientBuilder) SetNetwork(value string) *GrpcClientBuilder {
	b.network = value
	return b
}

// SetAddress sets the server address.
func (b *GrpcClientBuilder) SetAddress(value string) *GrpcClientBuilder {
	b.address = value
	return b
}

// SetHost sets the host name that the client will use for the TLS SNI extension and the HTTP Host header. This is
// optional, if not specified the host name from the address will be used.
func (b *GrpcClientBuilder) SetHost(value string) *GrpcClientBuilder {
	b.host = value
	return b
}

// SetPlaintext when set to true configures the client for a server that doesn't use TLS. The default is false.
func (b *GrpcClientBuilder) SetPlaintext(value bool) *GrpcClientBuilder {
	b.plaintext = value
	return b
}

// SetInsecure when set to true configures the client for use TLS but to not verify the certificate presented
// by the server. This shouldn't be used in production environments. The default is false.
func (b *GrpcClientBuilder) SetInsecure(value bool) *GrpcClientBuilder {
	b.insecure = value
	return b
}

// SetCaPool sets the certificate pool that contains the certificates of the certificate authorities that are trusted
// when connecting using TLS. This is optional, and the default is to use trust the certificate authorities trusted by
// the operating system.
func (b *GrpcClientBuilder) SetCaPool(value *x509.CertPool) *GrpcClientBuilder {
	b.caPool = value
	return b
}

// SetTokenSource sets the token source that the client will use to authenticate to the server. This is optional, by
// default no authentication credentials are sent.
func (b *GrpcClientBuilder) SetTokenSource(value auth.TokenSource) *GrpcClientBuilder {
	b.tokenSource = value
	return b
}

// SetKeepAlive sets the keep alive interval. This is optional, by default no keep alive is used.
func (b *GrpcClientBuilder) SetKeepAlive(value time.Duration) *GrpcClientBuilder {
	b.keepAlive = value
	return b
}

// SetKeepAliveTimeout sets the keep alive timeout. This is optional, by default 20 seconds is used.
func (b *GrpcClientBuilder) SetKeepAliveTimeout(value time.Duration) *GrpcClientBuilder {
	b.keepAliveTimeout = value
	return b
}

// SetUserAgent sets the 'User-Agent' header that will be sent with every request. This is optional, by default the
// gRPC library will use its own user agent string.
func (b *GrpcClientBuilder) SetUserAgent(value string) *GrpcClientBuilder {
	b.userAgent = value
	return b
}

// AddUnaryInterceptor adds a unary interceptor to the client.
func (b *GrpcClientBuilder) AddUnaryInterceptor(interceptor grpc.UnaryClientInterceptor) *GrpcClientBuilder {
	b.unaryInterceptors = append(b.unaryInterceptors, interceptor)
	return b
}

// AddUnaryInterceptors adds multiple unary interceptors to the client.
func (b *GrpcClientBuilder) AddUnaryInterceptors(interceptors ...grpc.UnaryClientInterceptor) *GrpcClientBuilder {
	b.unaryInterceptors = append(b.unaryInterceptors, interceptors...)
	return b
}

// AddStreamInterceptor adds a stream interceptor to the client.
func (b *GrpcClientBuilder) AddStreamInterceptor(interceptor grpc.StreamClientInterceptor) *GrpcClientBuilder {
	b.streamInterceptors = append(b.streamInterceptors, interceptor)
	return b
}

// AddStreamInterceptors adds multiple stream interceptors to the client.
func (b *GrpcClientBuilder) AddStreamInterceptors(interceptors ...grpc.StreamClientInterceptor) *GrpcClientBuilder {
	b.streamInterceptors = append(b.streamInterceptors, interceptors...)
	return b
}

// SetMetricsSubsystem sets the subsystem that will be used for metrics. This is optional, if not specified then no
// metrics will be collected.
func (b *GrpcClientBuilder) SetMetricsSubsystem(value string) *GrpcClientBuilder {
	b.metricsSubsystem = value
	return b
}

// SetMetricsRegiserer sets the metrics registry that will be used for metrics. This is optional, if not specified then
// the default metrics registry will be used.
func (b *GrpcClientBuilder) SetMetricsRegisterer(value prometheus.Registerer) *GrpcClientBuilder {
	b.metricsRegisterer = value
	return b
}

// Build uses the data stored in the builder to create a new network client.
func (b *GrpcClientBuilder) Build() (result *grpc.ClientConn, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.address == "" {
		err = errors.New("server address is mandatory")
		return
	}
	if b.keepAlive < 0 {
		err = fmt.Errorf("keep alive interval should be positive, but it is %s", b.keepAlive)
		return
	}

	// Set the default network:
	network := b.network
	if network == "" {
		network = "tcp"
	}

	// Calculate the endpoint:
	var endpoint string
	switch network {
	case "tcp":
		endpoint = fmt.Sprintf("dns:///%s", b.address)
	case "unix":
		if filepath.IsAbs(b.address) {
			endpoint = fmt.Sprintf("unix://%s", b.address)
		} else {
			endpoint = fmt.Sprintf("unix:%s", b.address)
		}
	default:
		err = fmt.Errorf("unknown network '%s'", b.network)
		return
	}

	// Set the default CA pool:
	caPool := b.caPool
	if caPool == nil {
		caPool, err = NewCertPool().
			SetLogger(b.logger).
			AddSystemFiles(true).
			AddKubernetesFiles(true).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to build CA pool: %w", err)
			return
		}
	}

	// Set the TLS options:
	var options []grpc.DialOption
	var transportCredentials credentials.TransportCredentials
	if b.plaintext {
		transportCredentials = insecure.NewCredentials()
	} else {
		tlsConfig := &tls.Config{}
		if b.insecure {
			tlsConfig.InsecureSkipVerify = true
		}
		if b.host != "" {
			tlsConfig.ServerName = b.host
		}
		tlsConfig.RootCAs = caPool

		// TODO: This should have been the non-experimental package, but we need to use this one because
		// currently the OpenShift router doesn't seem to support ALPN, and the regular credentials package
		// requires it since version 1.67. See here for details:
		//
		// https://github.com/grpc/grpc-go/issues/434
		// https://github.com/grpc/grpc-go/pull/7980
		//
		// Is there a way to configure the OpenShift router to avoid this?
		transportCredentials = experiementalcredentials.NewTLSWithALPNDisabled(tlsConfig)
	}
	if transportCredentials != nil {
		options = append(options, grpc.WithTransportCredentials(transportCredentials))
	}

	// Set the authority (HTTP Host header) if a host is specified:
	if b.host != "" {
		options = append(options, grpc.WithAuthority(b.host))
	}

	// Set the token options:
	if b.tokenSource != nil {
		var tokenCredentials credentials.PerRPCCredentials
		tokenCredentials, err = auth.NewTokenCredentials().
			SetLogger(b.logger).
			SetSource(b.tokenSource).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create token credentials: %w", err)
			return
		}
		options = append(options, grpc.WithPerRPCCredentials(tokenCredentials))
	}

	// Set the keep alive options:
	if b.keepAlive > 0 {
		timeout := b.keepAliveTimeout
		if timeout == 0 {
			timeout = 20 * time.Second
		}
		options = append(options, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    b.keepAlive,
			Timeout: timeout,
		}))
	}

	// Set the user agent option:
	if b.userAgent != "" {
		options = append(options, grpc.WithUserAgent(b.userAgent))
	}

	// Start with the interceptors configured by the user:
	unaryInterceptors := b.unaryInterceptors
	streamInterceptors := b.streamInterceptors

	// When a token source is configured, add interceptors that retry the request once when the server returns then
	// unauthenticated status code. This handles the case where the cached token has expired or been revoked: the
	// interceptor invalidates it and lets the next attempt fetch a fresh one.
	//
	// Note that currently we only do this for unary request, because streaming requests receive the error code from
	// the server when they finish, so in order to retry the request it is necessary to create a new stream.
	if b.tokenSource != nil {
		retry := &unauthRetryInterceptor{
			logger:      b.logger,
			tokenSource: b.tokenSource,
		}
		unaryInterceptors = append(
			[]grpc.UnaryClientInterceptor{
				retry.UnaryClient,
			},
			unaryInterceptors...,
		)
	}

	// Add the logging interceptor:
	loggingInterceptor, err := logging.NewInterceptor().
		SetLogger(b.logger).
		SetFlags(b.flags).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create logging interceptor: %w", err)
		return
	}
	unaryInterceptors = append(unaryInterceptors, loggingInterceptor.UnaryClient)
	streamInterceptors = append(streamInterceptors, loggingInterceptor.StreamClient)

	// Add the metrics interceptor:
	if b.metricsSubsystem != "" {
		var metricsInterceptor *metrics.GrpcInterceptor
		metricsInterceptor, err = metrics.NewGrpcInterceptor().
			SetSubsystem(b.metricsSubsystem).
			SetRegisterer(b.metricsRegisterer).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create metrics interceptor: %w", err)
			return
		}
		unaryInterceptors = append(unaryInterceptors, metricsInterceptor.UnaryClient)
		streamInterceptors = append(streamInterceptors, metricsInterceptor.StreamClient)
	}

	// Set the interceptor options:
	if len(unaryInterceptors) > 0 {
		options = append(options, grpc.WithChainUnaryInterceptor(unaryInterceptors...))
	}
	if len(streamInterceptors) > 0 {
		options = append(options, grpc.WithChainStreamInterceptor(streamInterceptors...))
	}

	// Create the client:
	result, err = grpc.NewClient(endpoint, options...)
	return
}

// unauthRetryInterceptor retries a gRPC call once when the server returns the unauthenticated status code. It
// invalidates the cached token before retrying.
type unauthRetryInterceptor struct {
	logger      *slog.Logger
	tokenSource auth.TokenSource
}

func (i *unauthRetryInterceptor) UnaryClient(ctx context.Context, method string, request, reply any,
	conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	callErr := invoker(ctx, method, request, reply, conn, opts...)
	if status.Code(callErr) != codes.Unauthenticated {
		return callErr
	}
	invalidateErr := i.tokenSource.Invalidate(ctx)
	if invalidateErr != nil {
		i.logger.ErrorContext(
			ctx,
			"Failed to invalidate token after unauthenticated response",
			slog.Any("error", invalidateErr),
		)
		return callErr
	}
	return invoker(ctx, method, request, reply, conn, opts...)
}

// Common client names:
const (
	GrpcClientName = "gRPC"
)
