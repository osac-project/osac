/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package rendering

import (
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Encode returns message as a generic JSON value with proto field names, ready to be written as JSON or YAML.
func Encode(message proto.Message) (any, error) {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// EncodeList returns every message as one generic JSON array, empty rather than null when there are none.
func EncodeList[T proto.Message](messages []T) ([]any, error) {
	values := make([]any, 0, len(messages))
	for _, message := range messages {
		value, err := Encode(message)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// EncodeWithType returns message as a generic JSON value with proto field names and a type URL in its @type field, so
// that a consumer can tell which message it received.
func EncodeWithType(message proto.Message) (any, error) {
	wrapper, err := anypb.New(message)
	if err != nil {
		return nil, err
	}
	return Encode(wrapper)
}

// EncodeListWithType returns every message as one generic JSON array of typed values, empty rather than null when
// there are none.
func EncodeListWithType[T proto.Message](messages []T) ([]any, error) {
	values := make([]any, len(messages))
	for i, message := range messages {
		value, err := EncodeWithType(message)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}
