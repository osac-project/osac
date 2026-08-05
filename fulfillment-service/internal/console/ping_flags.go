/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// AddPingFlags adds to the given flag set the flags needed to configure WebSocket ping parameters
// for the given connection side. The name (e.g. "client", "backend") is used as a prefix:
//
//	--console-client-ping-interval duration   Client ping interval. (default 30s)
//	--console-client-pong-timeout  duration   Client pong timeout. (default 10s)
//
// Setting ping interval to 0 disables ping entirely for that connection side.
func AddPingFlags(flags *pflag.FlagSet, name string) {
	defaults := DefaultPingConfig()
	_ = flags.Duration(
		pingFlagName(name, pingIntervalFlagSuffix),
		defaults.PingInterval,
		fmt.Sprintf(pingIntervalFlagHelp, name),
	)
	_ = flags.Duration(
		pingFlagName(name, pongTimeoutFlagSuffix),
		defaults.PongTimeout,
		fmt.Sprintf(pongTimeoutFlagHelp, name),
	)
}

// PingConfigFromFlags reads the ping flags for the given connection side and returns a
// populated PingConfig. It validates that the interval is non-negative and that the
// pong timeout is positive when pinging is enabled.
func PingConfigFromFlags(flags *pflag.FlagSet, name string) (PingConfig, error) {
	interval, err := flags.GetDuration(pingFlagName(name, pingIntervalFlagSuffix))
	if err != nil {
		return PingConfig{}, err
	}
	timeout, err := flags.GetDuration(pingFlagName(name, pongTimeoutFlagSuffix))
	if err != nil {
		return PingConfig{}, err
	}
	if interval < 0 {
		return PingConfig{}, fmt.Errorf(
			"%s ping interval must be non-negative, got %s",
			name, interval,
		)
	}
	if interval > 0 && timeout <= 0 {
		return PingConfig{}, fmt.Errorf(
			"%s pong timeout must be positive when ping is enabled, got %s",
			name, timeout,
		)
	}
	return PingConfig{
		PingInterval: interval,
		PongTimeout:  timeout,
	}, nil
}

// Names of the flag suffixes:
const (
	pingIntervalFlagSuffix = "ping-interval"
	pongTimeoutFlagSuffix  = "pong-timeout"
)

// pingFlagName builds a complete flag name from a connection side name and a flag suffix.
// For example, if the name is "client" and the suffix is "ping-interval" it returns
// "console-client-ping-interval".
func pingFlagName(name, suffix string) string {
	return fmt.Sprintf("console-%s-%s", strings.ToLower(name), suffix)
}

const pingIntervalFlagHelp = `
_DURATION_ - Interval between %s WebSocket ping messages sent to detect
dead connections. Set to 0 to disable pinging.
`

const pongTimeoutFlagHelp = `
_DURATION_ - Timeout for %s WebSocket pong responses. If a pong is not
received within this duration, the connection is closed.
`
