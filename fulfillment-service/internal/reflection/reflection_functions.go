/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"fmt"
	"reflect"
)

// NormalizeFunc takes a function value and wraps it into the canonical form func(context.Context) (T, error). The input
// function must match one of the following signatures, where T is the type parameter:
//
//   - func() T
//   - func(context.Context) T
//   - func() (T, error)
//   - func(context.Context) (T, error)
func NormalizeFunc[T any](fn any) (result func(context.Context) (T, error), err error) {
	// Check that the value is a non-nil function:
	fnType := reflect.TypeOf(fn)
	if fnType == nil || fnType.Kind() != reflect.Func {
		err = fmt.Errorf("expected a function, got %T", fn)
		return
	}
	fnValue := reflect.ValueOf(fn)
	if fnValue.IsNil() {
		err = fmt.Errorf("expected a non-nil function, got nil %s", fnType)
		return
	}
	resultType := reflect.TypeFor[T]()

	// Check if the function has a context parameter:
	var hasCtx, hasErr bool
	switch fnType.NumIn() {
	case 0:
		hasCtx = false
	case 1:
		if fnType.In(0) != contextType {
			err = fmt.Errorf(
				"function must have a context.Context parameter, but has %s",
				fnType.In(0),
			)
			return
		}
		hasCtx = true
	default:
		err = fmt.Errorf(
			"function must have at most one input parameter, but has %d",
			fnType.NumIn(),
		)
		return
	}

	// Check if the function has a error return value:
	switch fnType.NumOut() {
	case 1:
		if fnType.Out(0) != resultType {
			err = fmt.Errorf(
				"function must return %s, but returns %s",
				resultType, fnType.Out(0),
			)
			return
		}
		hasErr = false
	case 2:
		if fnType.Out(0) != resultType || fnType.Out(1) != errorType {
			err = fmt.Errorf(
				"function must return %s and error, but returns %s and %s",
				resultType, fnType.Out(0), fnType.Out(1),
			)
			return
		}
		hasErr = true
	default:
		err = fmt.Errorf(
			"function must return at most two values, but returns %d",
			fnType.NumOut(),
		)
		return
	}

	// Create a new function that calls the original function with the context parameter:
	result = func(ctx context.Context) (result T, err error) {
		var fnArgs []reflect.Value
		if hasCtx {
			fnArgs = []reflect.Value{
				reflect.ValueOf(ctx),
			}
		}
		fnResults := fnValue.Call(fnArgs)
		if hasErr && !fnResults[1].IsNil() {
			err = fnResults[1].Interface().(error)
			return
		}
		fnResult := fnResults[0].Interface()
		if fnResult != nil {
			result = fnResult.(T)
		}
		return
	}
	return
}

// Well-known reflection types:
var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)
