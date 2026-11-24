/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License
is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions and limitations under the
License.
*/

package quotausage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"google.golang.org/protobuf/reflect/protoreflect"
	"gopkg.in/yaml.v3"

	quota "github.com/osac-project/osac/proto/quota"
)

// countingRulesEnvVar names the environment variable that holds the path of the counting rules
// file, such as OSAC_QUOTA_COUNTING_RULES=/etc/osac/quota-counting-rules.yaml.
const countingRulesEnvVar = "OSAC_QUOTA_COUNTING_RULES"

// The counting rules are the release map of the quota state standard: the states in which a
// record of a kind provably holds nothing physical, so the counter stops counting it before the
// record is deleted and archived. The states are full proto enum value names, matched against
// the record's status.state JSON field. The file's shape:
//
//	counting:
//	  BareMetalInstance:
//	    stops_counting_in: [BARE_METAL_INSTANCE_STATE_FAILED]
//
// The example is documentation only; the shipped default is empty, so behavior is unchanged
// until the state standard lands.
//
// Safety: only states from which a record can become holding again through a new checked create
// may ever be mapped. A mapped record releases its quota without being deleted, so a state that
// a controller can leave by picking the hardware back up would let the record hold physical
// resources that no limit counts. The quota state standard owns that guarantee: terminal states
// revive only through a delete followed by a new create through the fulfillment API, where the
// quota check runs.
type countingRulesFile struct {
	Counting map[string]kindCountingRules `yaml:"counting"`
}

// kindCountingRules names the states in which one kind stops counting.
type kindCountingRules struct {
	StopsCountingIn []string `yaml:"stops_counting_in"`
}

// loadCountingRules reads the counting rules from the file named by the counting rules
// environment variable. An unset variable or a missing file means no rules; a file that exists
// but cannot be parsed is an error, so a broken release map is a startup failure, not a
// silently ignored one.
func loadCountingRules() (map[string][]string, error) {
	path := os.Getenv(countingRulesEnvVar)
	if path == "" {
		return nil, nil
	}
	// The path is operator configuration, the way the token file of the JWKS cache is.
	data, err := os.ReadFile(path) //nolint:gosec
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the counting rules file '%s': %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	// Unknown keys fail the load instead of silently dropping, the way the metering rules do:
	// a renamed key would otherwise load as no rule at all.
	decoder.KnownFields(true)
	var file countingRulesFile
	if err := decoder.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("invalid counting rules file '%s': %w", path, err)
	}
	rules := map[string][]string{}
	for kind, counting := range file.Counting {
		rules[kind] = counting.StopsCountingIn
	}
	return rules, nil
}

// checkCountingRules refuses rules the counter can't apply: a kind the quota registry doesn't
// know, a kind whose status has no state enum to match, and a state that isn't a value of that
// enum. Each failure names the file's content, so a typo is a startup failure, not a state that
// quietly counts forever.
func checkCountingRules(kinds map[string]*quota.Kind, rules map[string][]string) error {
	for kind, states := range rules {
		known := kinds[kind]
		if known == nil {
			return fmt.Errorf(
				"counting rules name kind '%s', which carries no quota annotations", kind)
		}
		state, err := known.Field("status.state")
		if err != nil || state == nil || state.Kind() != protoreflect.EnumKind {
			return fmt.Errorf(
				"counting rules name kind '%s', whose status.state is not an enum", kind)
		}
		values := state.Enum().Values()
		for _, name := range states {
			if values.ByName(protoreflect.Name(name)) == nil {
				return fmt.Errorf(
					"counting rules map kind '%s' to state '%s', which its status.state enum has no value of",
					kind, name)
			}
		}
	}
	return nil
}
