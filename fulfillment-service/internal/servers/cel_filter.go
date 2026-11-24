/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var celSyntaxEnv = func() *cel.Env {
	env, err := cel.NewEnv()
	if err != nil {
		panic(fmt.Sprintf("failed to create CEL environment: %v", err))
	}
	return env
}()

// validateCELSyntax checks that a filter string is a syntactically valid, complete CEL expression.
// This prevents filter bypass attacks where a malicious filter like "true) || (true" could
// break out of parenthesized composition and change operator precedence.
func validateCELSyntax(filter string) error {
	_, issues := celSyntaxEnv.Parse(filter)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("syntax error: %w", issues.Err())
	}
	return nil
}

// newTypedFilterEnv builds the CEL environment for evaluating a 'filter' expression against items of proto
// message type T, following the 'this' convention documented in docs/FILTER.md. The message type is fixed, so
// the result is built once at package scope; there is nothing per-request to configure.
func newTypedFilterEnv[T proto.Message]() *cel.Env {
	var model T
	desc := model.ProtoReflect().Descriptor()
	env, err := cel.NewEnv(
		cel.Types(model),
		cel.Variable("this", cel.ObjectType(string(desc.FullName()))),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create CEL filter environment for %s: %v", desc.FullName(), err))
	}
	return env
}

// filterItems returns the subset of items for which the CEL filter expression evaluates to true against 'this'.
// An empty filter matches every item. env comes from newTypedFilterEnv for the items' message type. It is the
// single in-memory filter pipeline shared by every server that filters a derived, non-DAO-backed list.
func filterItems[T proto.Message](ctx context.Context, env *cel.Env, items []T, filter string) ([]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filter == "" {
		return items, nil
	}
	tree, issues := env.Compile(filter)
	// Compilation does not observe the context, so a compile or type error may belong to a request that finished
	// while it ran. Reporting the request's own status keeps the caller's cancellation from being blamed on the
	// filter it sent.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	if !tree.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("filter result must be a boolean, but it is of type '%v'", tree.OutputType())
	}
	// ContextEval needs an interrupt frequency to observe cancellation inside CEL comprehensions.
	program, err := env.Program(tree, cel.InterruptCheckFrequency(100))
	// Building the program is context-blind as well, and the loop below runs zero times for an empty input, so
	// without this check a finished request would be reported as a successful empty result.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, _, err := program.ContextEval(ctx, map[string]any{"this": item})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		accepted, ok := value.Value().(bool)
		if !ok {
			return nil, fmt.Errorf("filter result must be a boolean, but it is of type '%T'", value.Value())
		}
		if accepted {
			result = append(result, item)
		}
	}
	return result, nil
}

// filterError reports a failed filter compilation or evaluation. A cancelled or expired request keeps its own
// transport status instead of being reported as an invalid filter, which would blame the caller's input for the
// caller's own cancellation.
func filterError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return status.Errorf(codes.InvalidArgument, "invalid filter: %s", err)
}

// filterReferencedFields parses a CEL filter and returns the set of all dotted field paths
// referenced in the expression (e.g. "this.spec.state", "this.metadata.name"). Paths that do not
// root at an identifier (e.g. function call results) are excluded.
func filterReferencedFields(filter string) (map[string]bool, error) {
	parsed, issues := celSyntaxEnv.Parse(filter)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("syntax error: %w", issues.Err())
	}
	fields := make(map[string]bool)
	nav := ast.NavigateAST(parsed.NativeRep())
	ast.MatchDescendants(nav, func(e ast.NavigableExpr) bool {
		if e.Kind() != ast.SelectKind {
			return false
		}
		path := resolveSelectPath(e.AsSelect())
		if path != "" {
			fields[path] = true
		}
		return false
	})
	return fields, nil
}

// fieldMatchesPrefix reports whether a dotted field path equals the prefix or is a direct child of
// it (e.g. "this.spec.state" matches prefix "this.spec.state", and "this.spec.state.sub" matches
// too, but "this.spec.state_machine" does not).
func fieldMatchesPrefix(field, prefix string) bool {
	return field == prefix || strings.HasPrefix(field, prefix+".")
}

// filterDefault pairs a field prefix with the CEL predicate to apply when the user's filter does
// not reference that field. Used with composeFilterDefaults for per-field default composition.
type filterDefault struct {
	field     string // field prefix to check, e.g. "this.spec.state"
	predicate string // CEL predicate applied when field is absent, e.g. "this.spec.state == 1"
}

// composeFilterDefaults composes per-field default predicates with a user-provided filter. For each
// default, the predicate is applied only if the user's filter does not reference that field. The
// filter is parsed and walked once regardless of the number of defaults.
func composeFilterDefaults(filter string, defaults []filterDefault) (string, error) {
	if filter == "" {
		clauses := make([]string, len(defaults))
		for i, d := range defaults {
			clauses[i] = d.predicate
		}
		return strings.Join(clauses, " && "), nil
	}

	fields, err := filterReferencedFields(filter)
	if err != nil {
		return "", fmt.Errorf("invalid filter: %w", err)
	}

	var missing []string
	for _, d := range defaults {
		referenced := false
		for field := range fields {
			if fieldMatchesPrefix(field, d.field) {
				referenced = true
				break
			}
		}
		if !referenced {
			missing = append(missing, d.predicate)
		}
	}

	if len(missing) == 0 {
		return filter, nil
	}
	return "(" + filter + ") && " + strings.Join(missing, " && "), nil
}

// resolveSelectPath reconstructs the full dotted field path from a chain of select expressions
// rooted at an identifier. For example, this.spec.state becomes "this.spec.state".
// Returns "" if the chain does not terminate at an identifier (e.g. a function call result).
func resolveSelectPath(sel ast.SelectExpr) string {
	var parts []string
	for {
		parts = append(parts, sel.FieldName())
		operand := sel.Operand()
		switch operand.Kind() {
		case ast.SelectKind:
			sel = operand.AsSelect()
		case ast.IdentKind:
			parts = append(parts, operand.AsIdent())
			// Reverse to get root-to-leaf order:
			for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
				parts[i], parts[j] = parts[j], parts[i]
			}
			return strings.Join(parts, ".")
		default:
			return ""
		}
	}
}
