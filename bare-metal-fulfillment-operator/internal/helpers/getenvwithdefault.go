// Package helpers contains small utility methods that don't belong anywhere else.
package helpers

import (
	"os"
	"strconv"
	"time"
)

// GetEnvWithDefault reads an environment variable, parses it to the specified type T,
// and returns the parsed value. If the environment variable is empty, unparsable, or
// fails validation, the defaultValue is returned instead.
//
// Supported types: string, int, bool, float64, time.Duration.
// For unsupported types, the default value is always returned.
func GetEnvWithDefault[T any](name string, defaultValue T, validator ...func(T) bool) (value T) {
	val := os.Getenv(name)
	if val == "" {
		return defaultValue
	}

	var result any
	var zero T
	var err error

	switch any(zero).(type) {
	case string:
		result = val
	case int:
		result, err = strconv.Atoi(val)
	case bool:
		result, err = strconv.ParseBool(val)
	case float64:
		result, err = strconv.ParseFloat(val, 64)
	case time.Duration:
		result, err = time.ParseDuration(val)
	default:
		return defaultValue
	}

	if err != nil {
		helperLog.Error(err, "conversion failed, using default", "envVar", name, "value", val, "default", defaultValue)
		return defaultValue
	}

	if len(validator) > 0 {
		for _, v := range validator {
			if !v(result.(T)) {
				helperLog.Info("validation failed, using default", "envVar", name, "value", val, "default", defaultValue)
				return defaultValue
			}
		}
	}

	return result.(T)
}
