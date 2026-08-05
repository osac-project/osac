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
	"reflect"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Path represents a path of a protobuf field mask, and simplifies getting and setting the value of that path. Don't
// create instances of this type directly, use a compiler to create it from text.
type Path[M proto.Message] struct {
	text  string
	steps []pathStep
}

// pathStepKind is the kind of step in a field path.
type pathStepKind int

const (
	pathStepKindField pathStepKind = iota
	pathStepKindListIndex
	pathStepKindMapKey
)

func (k pathStepKind) String() string {
	switch k {
	case pathStepKindField:
		return "field"
	case pathStepKindListIndex:
		return "index"
	case pathStepKindMapKey:
		return "key"
	}
	return ""
}

// pathStep represents each of the steps that need to be taken to get to a field of a message. For example,
// the path `a.b` would have two steps
//
// 1. Get the value of field `a`.
// 2. Get the value of field `b` from the message returned by the first step.
//
// That would be represented as:
//
//	pathStep{
//		kind: fieldPathStepKindField,
//		value: "a",
//	},
//	pathStep{
//		kind: fieldPathStepKindField,
//		value: "b",
//	},
type pathStep struct {
	kind  pathStepKind
	field protoreflect.FieldDescriptor
	key   any
	index int
}

// String returns the string representation of the step.
func (s pathStep) String() string {
	switch s.kind {
	case pathStepKindField:
		return fmt.Sprintf("field(%s)", s.field.Name())
	case pathStepKindListIndex:
		return fmt.Sprintf("index(%s, %d)", s.field.Name(), s.index)
	case pathStepKindMapKey:
		return fmt.Sprintf("key(%s, %v)", s.field.Name(), s.key)
	}
	return ""
}

// String retruns the string representation of the field path, which is the string passed when it was created.
func (p *Path[M]) String() string {
	return p.text
}

// Steps returns the steps that make up the path.
func (p *Path[M]) Steps() []pathStep {
	return slices.Clone(p.steps)
}

// Get returns the value of the field and a boolean flag indicating if the field is present.
func (p *Path[M]) Get(message M) (result protoreflect.Value, ok bool) {
	if p.isNil(message) {
		return
	}
	currentValue := protoreflect.ValueOfMessage(message.ProtoReflect())
	for _, currentStep := range p.steps {
		currentMessage := currentValue.Message()
		if !currentMessage.Has(currentStep.field) {
			return
		}
		currentValue = currentMessage.Get(currentStep.field)
		switch currentStep.kind {
		case pathStepKindListIndex:
			listItems := currentValue.List()
			if listItems.Len() < currentStep.index {
				return
			}
			currentValue = listItems.Get(currentStep.index)
		case pathStepKindMapKey:
			mapItems := currentValue.Map()
			mapKey := protoreflect.ValueOf(currentStep.key).MapKey()
			currentValue = mapItems.Get(mapKey)
			if !currentValue.IsValid() {
				return
			}
		}
	}
	result = currentValue
	ok = true
	return
}

// Set sets the the field to the given value.
func (p *Path[M]) Set(message M, value protoreflect.Value) {
	if p.isNil(message) {
		return
	}
	currentValue := protoreflect.ValueOfMessage(message.ProtoReflect())
	var currentMessage protoreflect.Message
	for i, currentStep := range p.steps {
		lastStep := i == len(p.steps)-1
		currentMessage = currentValue.Message()
		if !currentMessage.Has(currentStep.field) {
			currentValue = currentMessage.NewField(currentStep.field)
			currentMessage.Set(currentStep.field, currentValue)
		} else {
			currentValue = currentMessage.Get(currentStep.field)
		}
		switch currentStep.kind {
		case pathStepKindField:
			if lastStep {
				currentMessage.Set(currentStep.field, value)
			}
		case pathStepKindListIndex:
			listItems := currentValue.List()
			for listItems.Len() <= currentStep.index {
				listItems.Append(listItems.NewElement())
			}
			if lastStep {
				listItems.Set(currentStep.index, value)
			}
			currentValue = protoreflect.ValueOfList(listItems)
			currentMessage.Set(currentStep.field, currentValue)
		case pathStepKindMapKey:
			mapItems := currentValue.Map()
			mapKey := protoreflect.ValueOf(currentStep.key).MapKey()
			if lastStep {
				mapItems.Set(mapKey, value)
			} else {
				currentValue = mapItems.NewValue()
				mapItems.Set(mapKey, currentValue)
			}
		}
	}
}

// Clear clears the field, setting it to its default value.
func (p *Path[M]) Clear(message M) {
	if p.isNil(message) {
		return
	}
	currentValue := protoreflect.ValueOfMessage(message.ProtoReflect())
	for i, currentStep := range p.steps {
		lastStep := i == len(p.steps)-1
		currentMessage := currentValue.Message()
		if !currentMessage.Has(currentStep.field) {
			return
		}
		currentValue = currentMessage.Get(currentStep.field)
		switch currentStep.kind {
		case pathStepKindField:
			if lastStep {
				currentMessage.Clear(currentStep.field)
			}
		case pathStepKindListIndex:
			listItems := currentValue.List()
			if listItems.Len() < currentStep.index {
				return
			}
			if lastStep {
				listItems.Set(currentStep.index, listItems.NewElement())
			} else {
				currentValue = listItems.Get(currentStep.index)
			}
		case pathStepKindMapKey:
			mapItems := currentValue.Map()
			mapKey := protoreflect.ValueOf(currentStep.key).MapKey()
			if lastStep {
				mapItems.Clear(mapKey)
			} else {
				currentValue = mapItems.Get(mapKey)
			}
		}
	}
}

func (p *Path[M]) isNil(msg M) bool {
	return reflect.ValueOf(msg).IsNil()
}
