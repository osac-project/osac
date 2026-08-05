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

	"github.com/spf13/pflag"
)

// AddCorsFlags adds to the given flag set the flags needed to configure the CORS middleware. It receives the name of
// the listerner where the CORS support will be enabled. For example, to configure an API listener:
//
//	network.AddCorsFlags(flags, "API")
//
// The name will be converted to lower case to generate a prefix for the flags, and will be used unchanged as a prefix
// for the help text. The above example will result in the following flags:
//
//	--api-cors-allowed-origins strings API CORS allowed origins. (default "*")
func AddCorsFlags(flags *pflag.FlagSet, name string) {
	_ = flags.StringSlice(
		corsFlagName(name, corsAllowedOriginsFlagSuffix),
		[]string{
			"*",
		},
		fmt.Sprintf(corsAllowedOriginsFlagHelp, name),
	)
}

// Names of the flags:
const (
	corsAllowedOriginsFlagSuffix = "cors-allowed-origins"
)

// corsFlagName calculates a complete flag name from a listener name and a flag name suffix. For example, if the
// listener name is 'API' and the flag name suffix is 'allowed-origins' it returns 'api-cors-allowed-origins'.
func corsFlagName(name, suffix string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(name), suffix)
}

const corsAllowedOriginsFlagHelp = `
_[ORIGIN...]_ - %s CORS allowed origins.
`
