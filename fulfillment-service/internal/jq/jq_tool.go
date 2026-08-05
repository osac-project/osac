/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jq

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/itchyny/gojq"
	jsoniter "github.com/json-iterator/go"
)

// ToolBuilder contains the data needed to build a tool that knows how to run JQ queries. Don't create instances of this
// directly, use the NewTool function instead.
type ToolBuilder struct {
	logger        *slog.Logger
	compileOption *gojq.CompilerOption
}

// Tool knows how to run JQ queries. Don't create instances of this directly, use the NewTool function instead.
type Tool struct {
	logger        *slog.Logger
	lock          *sync.Mutex
	cache         map[string]*Query
	compileOption *gojq.CompilerOption
}

// NewTool creates a builder that can then be used to create a JQ tool.
func NewTool() *ToolBuilder {
	return &ToolBuilder{}
}

// SetLogger sets the logger that the JQ tool will use to write the log. This is mandatory.
func (b *ToolBuilder) SetLogger(value *slog.Logger) *ToolBuilder {
	b.logger = value
	return b
}

// SetCompilerOption sets the CompileOption to pass to gojq.Compile. This is optional.
func (b *ToolBuilder) SetCompilerOption(value *gojq.CompilerOption) *ToolBuilder {
	b.compileOption = value
	return b
}

// Build uses the information stored in the builder to create a new JQ tool.
func (b *ToolBuilder) Build() (result *Tool, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &Tool{
		logger:        b.logger,
		lock:          &sync.Mutex{},
		cache:         map[string]*Query{},
		compileOption: b.compileOption,
	}
	return
}

// Compile compiles the given query and saves it in a cache, so that evaluating the same query with the same variables
// later will not require compile it again.
func (t *Tool) Compile(source string, variables ...string) (result *Query, err error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	sort.Strings(variables)
	query, err := t.lookup(source, variables)
	if err != nil {
		return
	}
	if query == nil {
		query, err = t.compile(source, variables)
		if err != nil {
			return
		}
		t.cache[source] = query
	}
	result = query
	return
}

func (t *Tool) lookup(source string, variables []string) (result *Query, err error) {
	query, ok := t.cache[source]
	if !ok {
		return
	}
	if !slices.Equal(variables, query.variables) {
		err = fmt.Errorf(
			"query was compiled with variables %v but used with %v",
			query.variables, variables,
		)
		return
	}
	result = query
	return
}

func (t *Tool) compile(source string, variables []string) (query *Query, err error) {
	parsed, err := gojq.Parse(source)
	if err != nil {
		return
	}

	var code *gojq.Code
	if t.compileOption != nil {
		code, err = gojq.Compile(parsed, gojq.WithVariables(variables), *t.compileOption)
		if err != nil {
			return
		}
	} else {
		code, err = gojq.Compile(parsed, gojq.WithVariables(variables))
		if err != nil {
			return
		}
	}
	query = &Query{
		logger:    t.logger,
		source:    source,
		variables: variables,
		code:      code,
	}
	return
}

// Evaluate compiles the query and then evaluates it. The input can be any kind of object that can be serialized to
// JSON.
func (t *Tool) Evaluate(source string, input any, output any, variables ...Variable) error {
	slices.SortFunc(variables, func(a, b Variable) int {
		return strings.Compare(a.name, b.name)
	})
	names := make([]string, len(variables))
	values := make([]any, len(variables))
	for i, variable := range variables {
		names[i] = variable.name
		values[i] = variable.value
	}
	query, err := t.Compile(source, names...)
	if err != nil {
		return err
	}
	return query.evaluate(input, output, values)
}

// EvaluateString compiles the query and then evaluates it. The input should be a string containing a JSON document.
func (t *Tool) EvaluateString(source string, input string, output any, variables ...Variable) error {
	var tmp any
	err := jsoniter.Unmarshal([]byte(input), &tmp)
	if err != nil {
		return err
	}
	return t.Evaluate(source, tmp, output, variables...)
}

// EvaluateBytes compiles the query and then evaluates it. The input should be an array of bytes containing a JSON
// document.
func (t *Tool) EvaluateBytes(source string, input []byte, output any, variables ...Variable) error {
	var tmp any
	err := jsoniter.Unmarshal(input, &tmp)
	if err != nil {
		return err
	}
	return t.Evaluate(source, tmp, output, variables...)
}
