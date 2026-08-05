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
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"

	"github.com/itchyny/gojq"
	jsoniter "github.com/json-iterator/go"
)

// Query contains the data and logic needed to evaluate a query. Instances are created using the Compile or Evaluate
// methods of the Tool type.
type Query struct {
	logger    *slog.Logger
	source    string
	variables []string
	code      *gojq.Code
}

// Evaluate evaluates the query on the given input. The output should be a pointer to a variable where the result will
// be stored. Optional named variables can be passed.
func (q *Query) Evaluate(input any, output any, variables ...Variable) error {
	slices.SortFunc(variables, func(a, b Variable) int {
		return strings.Compare(a.name, b.name)
	})
	names := make([]string, len(variables))
	values := make([]any, len(variables))
	for i, variable := range variables {
		names[i] = variable.name
		values[i] = variable.value
	}
	if !reflect.DeepEqual(names, q.variables) {
		return fmt.Errorf(
			"query was compiled with variables %v but used with %v",
			q.variables, names,
		)
	}
	return q.evaluate(input, output, values)
}

func (q *Query) evaluate(input any, output any, variables []any) error {
	// Check that the output is a pointer:
	outputType := reflect.TypeOf(output)
	if outputType.Kind() != reflect.Pointer {
		return fmt.Errorf("output should be a pointer, but it is of type '%T'", output)
	}

	// The library that we use expects the input to bo composed only of primitive types, slices and maps, but we
	// want to support other input types, like structs. To achieve that we serialize the input and deserialize it
	// again.
	var tmp any
	err := q.clone(input, &tmp)
	if err != nil {
		return err
	}
	input = tmp

	// Run the query:
	var results []any
	iter := q.code.Run(input, variables...)
	for {
		result, ok := iter.Next()
		if !ok {
			break
		}
		err, ok = result.(error)
		if ok {
			return err
		}
		results = append(results, result)
	}

	// If the output isn't a slice then we take only the first result:
	var result any
	if outputType.Elem().Kind() == reflect.Slice {
		result = results
	} else {
		length := len(results)
		if length == 0 {
			return fmt.Errorf("query produced no results")
		}
		if length > 1 {
			q.logger.Warn(
				"Query produced multiple results but output type isn't a slice, "+
					"will return the first result",
				slog.String("query", q.source),
				slog.String("type", fmt.Sprintf("%T", output)),
				slog.Any("results", results),
			)
		}
		result = results[0]
	}

	// Copy the result to the output:
	return q.clone(result, output)
}

func (q *Query) clone(input any, output any) error {
	data, err := jsoniter.Marshal(input)
	if err != nil {
		return err
	}
	return jsoniter.Unmarshal(data, output)
}
