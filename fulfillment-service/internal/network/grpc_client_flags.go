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
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// AddGrpcClientFlags adds to the given flag set the flags needed to configure a network client. It receives the name of
// the client and the default server address. For example, to configure an API client:
//
//	network.AddGrpcClientFlags(flags, "API", "localhost:8000")
//
// The name will be converted to lower case to generate a prefix for the flags, and will be used unchanged as a prefix
// for the help text. The above example will result in the following flags:
//
//	--api-server-network string API server network. (default "tcp")
//	--api-server-address string API server address. (default "localhost:8000")
//	--api-server-plaintext      API disable TLS.
//	--api-server-insecure       API disable TLS certificate validation.
//	--api-keep-alive            API keep alive interval.
func AddGrpcClientFlags(flags *pflag.FlagSet, name, addr string) {
	_ = flags.String(
		grpcClientFlagName(name, grpcClientServerNetworkFlagSuffix),
		"tcp",
		fmt.Sprintf(grpcClientServerNetworkFlagHelp, name),
	)
	_ = flags.String(
		grpcClientFlagName(name, grpcClientServerAddrFlagSuffix),
		addr,
		fmt.Sprintf(grpcClientServerAddrFlagHelp, name),
	)
	_ = flags.Bool(
		grpcClientFlagName(name, grpcClientServerPlaintextFlagSuffix),
		false,
		fmt.Sprintf(grpcClientServerPlaintextFlagHelp, name),
	)
	_ = flags.Bool(
		grpcClientFlagName(name, grpcClientServerInsecureFlagSuffix),
		false,
		fmt.Sprintf(grpcClientServerInsecureFlagHelp, name),
	)
	_ = flags.Duration(
		grpcClientFlagName(name, grpcClientKeepAliveFlagSuffix),
		5*time.Minute,
		fmt.Sprintf(grpcClientKeepAliveFlagHelp, name),
	)
}

// Names of the flags:
const (
	grpcClientServerNetworkFlagSuffix   = "server-network"
	grpcClientServerAddrFlagSuffix      = "server-address"
	grpcClientServerPlaintextFlagSuffix = "server-plaintext"
	grpcClientServerInsecureFlagSuffix  = "server-insecure"
	grpcClientKeepAliveFlagSuffix       = "keep-alive"
)

// grpcClientFlagName calculates a complete flag name from a client name and a flag name suffix. For example, if the
// client name is 'API' and the flag name suffix is 'server-address' it returns 'api-server-address'.
func grpcClientFlagName(name, suffix string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(name), suffix)
}

const grpcClientServerNetworkFlagHelp = `
_NETWORK_ - %s server network.
`

const grpcClientServerAddrFlagHelp = `
_ADDRESS_ - %s server address.
`

const grpcClientServerPlaintextFlagHelp = `
_[BOOLEAN]_ - %s enable or disable TLS.
`

const grpcClientServerInsecureFlagHelp = `
_[BOOLEAN]_ - %s enable or disable TLS certificate validation.
`

const grpcClientKeepAliveFlagHelp = `
_DURATION_ - %s keep alive interval.
`
