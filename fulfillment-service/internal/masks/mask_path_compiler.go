/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package masks

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// PathCompilerBuilder contains the data and logic need to build a path compiler.
type PathCompilerBuilder[M proto.Message] struct {
	logger *slog.Logger
}

// PathCompiler compiles a field path into an object that can be used to get, set and clear the value of a path.
type PathCompiler[M proto.Message] struct {
	logger *slog.Logger
	desc   protoreflect.MessageDescriptor
}

// NewPathCompiler creates a builder for a field path compiler.
func NewPathCompiler[M proto.Message]() *PathCompilerBuilder[M] {
	return &PathCompilerBuilder[M]{
		logger: slog.Default(),
	}
}

// SetLogger sets the logger to be used by the compiler. This is mandatory.
func (b *PathCompilerBuilder[M]) SetLogger(logger *slog.Logger) *PathCompilerBuilder[M] {
	b.logger = logger
	return b
}

// Build uses the data in the builder to create the compiler.
func (b *PathCompilerBuilder[M]) Build() (result *PathCompiler[M], err error) {
	// Check the parameters:
	if b.logger == nil {
		return nil, fmt.Errorf("logger is mandatory")
	}

	// Get the message descriptor:
	var message M
	desc := message.ProtoReflect().Descriptor()

	// Create the result:
	result = &PathCompiler[M]{
		logger: b.logger,
		desc:   desc,
	}
	return
}

// Compile compiles the field path from the given string.
func (c *PathCompiler[M]) Compile(path string) (result *Path[M], err error) {
	// Split the path into steps:
	segments := c.split(path)
	steps := make([]pathStep, 0, len(segments))
	desc := c.desc
	for len(segments) > 0 {
		field := desc.Fields().ByName(protoreflect.Name(segments[0]))
		if field == nil {
			err = fmt.Errorf("field '%s' not found", segments[0])
			return
		}
		step := pathStep{
			kind:  pathStepKindField,
			field: field,
		}
		switch {
		case field.IsList():
			if len(segments) > 1 {
				var index int
				index, err = strconv.Atoi(segments[1])
				if err != nil {
					err = fmt.Errorf("invalid index '%s': %w", segments[1], err)
					return
				}
				step.kind = pathStepKindListIndex
				step.index = index
				segments = segments[2:]
			} else {
				segments = segments[1:]
			}
			if field.Kind() == protoreflect.MessageKind {
				desc = field.Message()
			}
		case field.IsMap():
			if len(segments) > 1 {
				step.kind = pathStepKindMapKey
				step.key = segments[1]
				segments = segments[2:]
			} else {
				segments = segments[1:]
			}
			item := field.MapValue()
			if item.Kind() == protoreflect.MessageKind {
				desc = item.Message()
			}
		default:
			segments = segments[1:]
			if field.Kind() == protoreflect.MessageKind {
				desc = field.Message()
			}
		}
		steps = append(steps, step)
	}

	// Normalize the path:
	path = strings.Join(segments, ".")

	// Create the result:
	result = &Path[M]{
		text:  path,
		steps: steps,
	}
	return
}

func (c *PathCompiler[M]) split(path string) []string {
	chunks := strings.Split(path, ".")
	for i, chunk := range chunks {
		chunks[i] = strings.TrimSpace(chunk)
	}
	result := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != "" {
			result = append(result, chunk)
		}
	}
	return result
}
