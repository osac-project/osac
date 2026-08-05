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
	"fmt"
	"time"

	"github.com/spf13/pflag"
)

// GrpcKeepaliveConfig holds the gRPC server keepalive parameters.
type GrpcKeepaliveConfig struct {
	Time    time.Duration
	Timeout time.Duration
	MinTime time.Duration
}

// DefaultGrpcKeepaliveConfig returns a GrpcKeepaliveConfig with the default values.
func DefaultGrpcKeepaliveConfig() GrpcKeepaliveConfig {
	return GrpcKeepaliveConfig{
		Time:    15 * time.Second,
		Timeout: 10 * time.Second,
		MinTime: 10 * time.Second,
	}
}

// AddGrpcKeepaliveFlags adds to the given flag set the flags needed to configure gRPC server
// keepalive parameters.
//
//	--grpc-keepalive-time     duration   gRPC server keepalive ping interval. (default 15s)
//	--grpc-keepalive-timeout  duration   gRPC server keepalive ping timeout. (default 10s)
//	--grpc-keepalive-min-time duration   Minimum keepalive interval allowed from clients. (default 10s)
func AddGrpcKeepaliveFlags(flags *pflag.FlagSet) {
	defaults := DefaultGrpcKeepaliveConfig()
	_ = flags.Duration(
		grpcKeepaliveTimeFlagName,
		defaults.Time,
		grpcKeepaliveTimeFlagHelp,
	)
	_ = flags.Duration(
		grpcKeepaliveTimeoutFlagName,
		defaults.Timeout,
		grpcKeepaliveTimeoutFlagHelp,
	)
	_ = flags.Duration(
		grpcKeepaliveMinTimeFlagName,
		defaults.MinTime,
		grpcKeepaliveMinTimeFlagHelp,
	)
}

// GrpcKeepaliveConfigFromFlags reads the gRPC keepalive flags and returns a populated config.
// All durations must be positive.
func GrpcKeepaliveConfigFromFlags(flags *pflag.FlagSet) (GrpcKeepaliveConfig, error) {
	t, err := flags.GetDuration(grpcKeepaliveTimeFlagName)
	if err != nil {
		return GrpcKeepaliveConfig{}, err
	}
	timeout, err := flags.GetDuration(grpcKeepaliveTimeoutFlagName)
	if err != nil {
		return GrpcKeepaliveConfig{}, err
	}
	minTime, err := flags.GetDuration(grpcKeepaliveMinTimeFlagName)
	if err != nil {
		return GrpcKeepaliveConfig{}, err
	}
	if t <= 0 {
		return GrpcKeepaliveConfig{}, fmt.Errorf(
			"gRPC keepalive time must be positive, got %s", t,
		)
	}
	if timeout <= 0 {
		return GrpcKeepaliveConfig{}, fmt.Errorf(
			"gRPC keepalive timeout must be positive, got %s", timeout,
		)
	}
	if minTime <= 0 {
		return GrpcKeepaliveConfig{}, fmt.Errorf(
			"gRPC keepalive minimum time must be positive, got %s", minTime,
		)
	}
	return GrpcKeepaliveConfig{
		Time:    t,
		Timeout: timeout,
		MinTime: minTime,
	}, nil
}

const (
	grpcKeepaliveTimeFlagName    = "grpc-keepalive-time"
	grpcKeepaliveTimeoutFlagName = "grpc-keepalive-timeout"
	grpcKeepaliveMinTimeFlagName = "grpc-keepalive-min-time"
)

const grpcKeepaliveTimeFlagHelp = `
_DURATION_ - After this duration of inactivity the gRPC server sends a
keepalive ping to clients.
`

const grpcKeepaliveTimeoutFlagHelp = `
_DURATION_ - After sending a keepalive ping, the server waits this long
for a response before closing the connection.
`

const grpcKeepaliveMinTimeFlagHelp = `
_DURATION_ - Minimum interval clients must wait between sending keepalive
pings. Clients that ping more frequently will be disconnected.
`
