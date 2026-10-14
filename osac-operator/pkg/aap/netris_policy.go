package aap

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

const maxProtectedInputDepth = 32
const maxProtectedInputNodes = 10000

//go:embed netris_protected_inputs_contract.json
var protectedInputsContract []byte

type protectedInputContract struct {
	PolicyVersion   string `json:"policy_version"`
	ProtectedInputs []struct {
		Aliases              []string `json:"aliases"`
		CanonicalEnvironment string   `json:"canonical_environment"`
	} `json:"protected_inputs"`
}

var protectedContract = loadProtectedInputContract()

var protectedNames = protectedInputNames()

// ProtectedInputError reports a protected Netris launch key or malformed input.
type ProtectedInputError struct {
	Path          string
	Key           string
	PolicyVersion string
	Reason        string
}

func (e *ProtectedInputError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s at %s (policy %s)", e.Reason, e.Path, e.PolicyVersion)
	}
	return fmt.Sprintf("%s %q at %s (policy %s)", e.Reason, e.Key, e.Path, e.PolicyVersion)
}

func loadProtectedInputContract() protectedInputContract {
	var contract protectedInputContract
	if err := json.Unmarshal(protectedInputsContract, &contract); err != nil {
		panic(fmt.Sprintf("invalid protected Netris input contract: %v", err))
	}
	return contract
}

func normalizeProtectedInputKey(value string) string {
	var normalized strings.Builder
	for _, r := range value {
		if r == '_' || r == '-' || r == '.' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteByte(byte(r))
		}
	}
	return normalized.String()
}

func protectedInputNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, input := range protectedContract.ProtectedInputs {
		for _, alias := range input.Aliases {
			names[normalizeProtectedInputKey(alias)] = struct{}{}
		}
		names[normalizeProtectedInputKey(input.CanonicalEnvironment)] = struct{}{}
	}
	return names
}

func protectedPath(path []string) bool {
	for index := range path {
		var candidate strings.Builder
		for _, component := range path[index:] {
			candidate.WriteString(component)
		}
		if _, ok := protectedNames[normalizeProtectedInputKey(candidate.String())]; ok {
			return true
		}
	}
	return false
}

func protectedPathText(path []string) string {
	result := "$"
	for _, component := range path {
		result += fmt.Sprintf("[%q]", component)
	}
	return result
}

// ValidateProtectedNetrisExtraVars rejects protected names recursively without mutation.
func ValidateProtectedNetrisExtraVars(extraVars map[string]any) error {
	if extraVars == nil {
		return &ProtectedInputError{
			Path:          "$",
			PolicyVersion: protectedContract.PolicyVersion,
			Reason:        "extra_vars must be a mapping",
		}
	}
	state := 0
	return walkProtectedInputValue(reflect.ValueOf(extraVars), nil, 0, &state)
}

func walkProtectedInputValue(value reflect.Value, path []string, depth int, nodes *int) error {
	if depth > maxProtectedInputDepth {
		return &ProtectedInputError{
			Path:          protectedPathText(path),
			PolicyVersion: protectedContract.PolicyVersion,
			Reason:        "extra_vars nesting exceeds the supported limit",
		}
	}
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return walkProtectedInputValue(value.Elem(), path, depth, nodes)
	}
	*nodes++
	if *nodes > maxProtectedInputNodes {
		return &ProtectedInputError{
			Path:          protectedPathText(path),
			PolicyVersion: protectedContract.PolicyVersion,
			Reason:        "extra_vars contains too many values",
		}
	}

	switch value.Kind() {
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			key := iter.Key()
			if key.Kind() != reflect.String {
				return &ProtectedInputError{
					Path:          protectedPathText(path),
					PolicyVersion: protectedContract.PolicyVersion,
					Reason:        "extra_vars keys must be strings",
				}
			}
			name := key.String()
			childPath := append(append([]string{}, path...), name)
			if protectedPath(childPath) {
				return &ProtectedInputError{
					Path:          protectedPathText(childPath),
					Key:           name,
					PolicyVersion: protectedContract.PolicyVersion,
					Reason:        "protected Netris setting is not allowed",
				}
			}
			if err := walkProtectedInputValue(iter.Value(), childPath, depth+1, nodes); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := walkProtectedInputValue(value.Index(index), path, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}
