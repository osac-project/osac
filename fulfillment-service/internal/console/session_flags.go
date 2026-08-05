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
	"time"

	"github.com/spf13/pflag"
)

// DefaultSessionTimeout is the default maximum duration for a console session.
const DefaultSessionTimeout = 30 * time.Minute

// AddSessionTimeoutFlag adds to the given flag set the flag for configuring the console session
// timeout.
//
//	--console-session-timeout duration   Maximum duration for a console session. (default 30m)
func AddSessionTimeoutFlag(flags *pflag.FlagSet) {
	_ = flags.Duration(
		sessionTimeoutFlagName,
		DefaultSessionTimeout,
		sessionTimeoutFlagHelp,
	)
}

// SessionTimeoutFromFlags reads the session timeout flag and validates that
// the value is positive.
func SessionTimeoutFromFlags(flags *pflag.FlagSet) (time.Duration, error) {
	timeout, err := flags.GetDuration(sessionTimeoutFlagName)
	if err != nil {
		return 0, err
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("session timeout must be positive, got %s", timeout)
	}
	return timeout, nil
}

const sessionTimeoutFlagName = "console-session-timeout"

const sessionTimeoutFlagHelp = `
_DURATION_ - Maximum duration for a console session. The session is
terminated after this duration regardless of activity.
`
