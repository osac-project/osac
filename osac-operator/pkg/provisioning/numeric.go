/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provisioning

import (
	"encoding/json"
	"fmt"
	"math"
)

func int64FromNumber(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		return int64FromUnsigned(uint64(typed))
	case uint8:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint64:
		return int64FromUnsigned(typed)
	case uintptr:
		return int64FromUnsigned(uint64(typed))
	case float32:
		return int64FromFloat(float64(typed))
	case float64:
		return int64FromFloat(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("not an int64: %w", err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func int64FromUnsigned(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("unsigned value %d exceeds int64", value)
	}
	return int64(value), nil
}

func int64FromFloat(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, fmt.Errorf("value %v is not a finite integer", value)
	}
	// The upper bound is exclusive because float64(math.MaxInt64) rounds up to
	// 2^63, while -2^63 itself is representable and valid.
	if value < float64(math.MinInt64) || value >= -float64(math.MinInt64) {
		return 0, fmt.Errorf("value %v is outside the int64 range", value)
	}
	return int64(value), nil
}
