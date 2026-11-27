/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package match

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

// Subscription CEL attrs. Keep in sync with
// fulfillment-service/internal/servers/subscription_filter.go.
var filterBindings = []struct {
	name string
	get  func(*cevent.Accepted) string
}{
	{"kind", func(a *cevent.Accepted) string { return a.Kind }},
	{"severity", func(a *cevent.Accepted) string { return a.Severity }},
	{"event_type", func(a *cevent.Accepted) string { return a.Type }},
	{"title", func(a *cevent.Accepted) string { return a.Data.Title }},
	{"tenant", func(a *cevent.Accepted) string { return a.Tenant }},
}

var (
	envOnce sync.Once
	env     *cel.Env
	envErr  error
)

func getEnv() (*cel.Env, error) {
	envOnce.Do(func() {
		opts := make([]cel.EnvOption, 0, len(filterBindings))
		for _, b := range filterBindings {
			opts = append(opts, cel.Variable(b.name, cel.StringType))
		}
		env, envErr = cel.NewEnv(opts...)
	})
	return env, envErr
}

func activation(accepted *cevent.Accepted) map[string]any {
	out := make(map[string]any, len(filterBindings))
	for _, b := range filterBindings {
		out[b.name] = b.get(accepted)
	}
	return out
}

// EvalFilter evaluates a subscription CEL filter against an accepted CloudEvent.
func EvalFilter(filter string, accepted *cevent.Accepted) (bool, error) {
	if filter == "" {
		return false, fmt.Errorf("empty filter")
	}
	celEnv, err := getEnv()
	if err != nil {
		return false, err
	}
	ast, issues := celEnv.Compile(filter)
	if issues != nil && issues.Err() != nil {
		return false, issues.Err()
	}
	prg, err := celEnv.Program(ast)
	if err != nil {
		return false, err
	}
	out, _, err := prg.Eval(activation(accepted))
	if err != nil {
		return false, err
	}
	v, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("filter did not evaluate to bool")
	}
	return v, nil
}
