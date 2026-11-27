/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
)

// Subscription CEL attrs. Keep in sync with
// osac-notifications/internal/match/filter.go.
var subscriptionFilterAttrs = []string{
	"kind",
	"severity",
	"event_type",
	"title",
	"tenant",
}

var (
	subscriptionFilterEnvOnce sync.Once
	subscriptionFilterEnv     *cel.Env
	subscriptionFilterEnvErr  error
)

func getSubscriptionFilterEnv() (*cel.Env, error) {
	subscriptionFilterEnvOnce.Do(func() {
		opts := make([]cel.EnvOption, 0, len(subscriptionFilterAttrs))
		for _, name := range subscriptionFilterAttrs {
			opts = append(opts, cel.Variable(name, cel.StringType))
		}
		subscriptionFilterEnv, subscriptionFilterEnvErr = cel.NewEnv(opts...)
	})
	return subscriptionFilterEnv, subscriptionFilterEnvErr
}

// validateSubscriptionFilter compiles a subscription CEL filter against the
// event-shaped variables used for webhook matching.
func validateSubscriptionFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("filter is required")
	}
	env, err := getSubscriptionFilterEnv()
	if err != nil {
		return fmt.Errorf("subscription filter environment: %w", err)
	}
	ast, issues := env.Compile(filter)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("invalid filter: %w", issues.Err())
	}
	_, err = env.Program(ast)
	if err != nil {
		return fmt.Errorf("invalid filter: %w", err)
	}
	return nil
}
