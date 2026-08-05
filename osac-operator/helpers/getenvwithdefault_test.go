package helpers

import (
	"os"
	"testing"
	"time"
)

// TestGetEnvWithDefault_BackwardCompatibility ensures existing behavior without validators
func TestGetEnvWithDefault_BackwardCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		envValue     string
		defaultValue any
		expected     any
	}{
		{"string with env set", "TEST_STRING", "hello", "default", "hello"},
		{"string with empty env", "TEST_EMPTY", "", "default", "default"},
		{"int with valid value", "TEST_INT", "42", 10, 42},
		{"int with invalid value", "TEST_INT_BAD", "notanumber", 10, 10},
		{"bool true", "TEST_BOOL_TRUE", "true", false, true},
		{"bool false", "TEST_BOOL_FALSE", "false", true, false},
		{"bool invalid", "TEST_BOOL_BAD", "notabool", true, true},
		{"float64 valid", "TEST_FLOAT", "3.14", 1.0, 3.14},
		{"float64 invalid", "TEST_FLOAT_BAD", "notafloat", 1.0, 1.0},
		{"duration valid", "TEST_DURATION", "5s", time.Second, 5 * time.Second},
		{"duration invalid", "TEST_DURATION_BAD", "notaduration", time.Second, time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			} else {
				os.Unsetenv(tt.envVar)
			}

			switch expected := tt.expected.(type) {
			case string:
				result := GetEnvWithDefault(tt.envVar, tt.defaultValue.(string))
				if result != expected {
					t.Errorf("expected %v, got %v", expected, result)
				}
			case int:
				result := GetEnvWithDefault(tt.envVar, tt.defaultValue.(int))
				if result != expected {
					t.Errorf("expected %v, got %v", expected, result)
				}
			case bool:
				result := GetEnvWithDefault(tt.envVar, tt.defaultValue.(bool))
				if result != expected {
					t.Errorf("expected %v, got %v", expected, result)
				}
			case float64:
				result := GetEnvWithDefault(tt.envVar, tt.defaultValue.(float64))
				if result != expected {
					t.Errorf("expected %v, got %v", expected, result)
				}
			case time.Duration:
				result := GetEnvWithDefault(tt.envVar, tt.defaultValue.(time.Duration))
				if result != expected {
					t.Errorf("expected %v, got %v", expected, result)
				}
			}
		})
	}
}

// TestGetEnvWithDefault_SingleValidatorPass tests that valid values pass single validator
func TestGetEnvWithDefault_SingleValidatorPass(t *testing.T) {
	os.Setenv("TEST_PORT", "8080")
	defer os.Unsetenv("TEST_PORT")

	validator := func(v int) bool { return v > 0 && v < 65536 }
	result := GetEnvWithDefault("TEST_PORT", 9000, validator)

	if result != 8080 {
		t.Errorf("expected 8080, got %v", result)
	}
}

// TestGetEnvWithDefault_SingleValidatorFail tests that invalid values use default
func TestGetEnvWithDefault_SingleValidatorFail(t *testing.T) {
	os.Setenv("TEST_PORT", "99999")
	defer os.Unsetenv("TEST_PORT")

	validator := func(v int) bool { return v > 0 && v < 65536 }
	result := GetEnvWithDefault("TEST_PORT", 8080, validator)

	if result != 8080 {
		t.Errorf("expected default 8080, got %v", result)
	}
}

// TestGetEnvWithDefault_MultipleValidatorsPass tests that all validators must pass
func TestGetEnvWithDefault_MultipleValidatorsPass(t *testing.T) {
	os.Setenv("TEST_PORT", "8080")
	defer os.Unsetenv("TEST_PORT")

	isPositive := func(v int) bool { return v > 0 }
	isValidPort := func(v int) bool { return v < 65536 }

	result := GetEnvWithDefault("TEST_PORT", 9000, isPositive, isValidPort)

	if result != 8080 {
		t.Errorf("expected 8080, got %v", result)
	}
}

// TestGetEnvWithDefault_MultipleValidatorsFirstFails tests AND logic (short-circuit on first failure)
func TestGetEnvWithDefault_MultipleValidatorsFirstFails(t *testing.T) {
	os.Setenv("TEST_VALUE", "-5")
	defer os.Unsetenv("TEST_VALUE")

	isPositive := func(v int) bool { return v > 0 }
	isLessThan100 := func(v int) bool { return v < 100 }

	result := GetEnvWithDefault("TEST_VALUE", 50, isPositive, isLessThan100)

	if result != 50 {
		t.Errorf("expected default 50, got %v", result)
	}
}

// TestGetEnvWithDefault_MultipleValidatorsSecondFails tests AND logic (second validator fails)
func TestGetEnvWithDefault_MultipleValidatorsSecondFails(t *testing.T) {
	os.Setenv("TEST_VALUE", "150")
	defer os.Unsetenv("TEST_VALUE")

	isPositive := func(v int) bool { return v > 0 }
	isLessThan100 := func(v int) bool { return v < 100 }

	result := GetEnvWithDefault("TEST_VALUE", 50, isPositive, isLessThan100)

	if result != 50 {
		t.Errorf("expected default 50, got %v", result)
	}
}

// TestGetEnvWithDefault_EmptyEnvDoesNotCallValidator ensures validators aren't called for empty env vars
func TestGetEnvWithDefault_EmptyEnvDoesNotCallValidator(t *testing.T) {
	os.Unsetenv("TEST_MISSING")

	validatorCalled := false
	validator := func(v int) bool {
		validatorCalled = true
		return true
	}

	result := GetEnvWithDefault("TEST_MISSING", 42, validator)

	if result != 42 {
		t.Errorf("expected default 42, got %v", result)
	}
	if validatorCalled {
		t.Error("validator should not be called for empty env var")
	}
}

// TestGetEnvWithDefault_ParseFailureDoesNotCallValidator ensures validators aren't called for parse failures
func TestGetEnvWithDefault_ParseFailureDoesNotCallValidator(t *testing.T) {
	os.Setenv("TEST_INVALID", "notanumber")
	defer os.Unsetenv("TEST_INVALID")

	validatorCalled := false
	validator := func(v int) bool {
		validatorCalled = true
		return true
	}

	result := GetEnvWithDefault("TEST_INVALID", 42, validator)

	if result != 42 {
		t.Errorf("expected default 42, got %v", result)
	}
	if validatorCalled {
		t.Error("validator should not be called for parse failure")
	}
}

// TestGetEnvWithDefault_StringValidation tests validation with string type
func TestGetEnvWithDefault_StringValidation(t *testing.T) {
	os.Setenv("TEST_MODE", "production")
	defer os.Unsetenv("TEST_MODE")

	validModes := func(v string) bool {
		return v == "development" || v == "staging" || v == "production"
	}

	result := GetEnvWithDefault("TEST_MODE", "development", validModes)
	if result != "production" {
		t.Errorf("expected production, got %v", result)
	}

	// Test invalid value
	os.Setenv("TEST_MODE", "invalid")
	result = GetEnvWithDefault("TEST_MODE", "development", validModes)
	if result != "development" {
		t.Errorf("expected default development, got %v", result)
	}
}

// TestGetEnvWithDefault_BoolValidation tests validation with bool type
func TestGetEnvWithDefault_BoolValidation(t *testing.T) {
	os.Setenv("TEST_ENABLED", "true")
	defer os.Unsetenv("TEST_ENABLED")

	mustBeTrue := func(v bool) bool { return v == true }

	result := GetEnvWithDefault("TEST_ENABLED", false, mustBeTrue)
	if result != true {
		t.Errorf("expected true, got %v", result)
	}

	// Test invalid value (false fails validation)
	os.Setenv("TEST_ENABLED", "false")
	result = GetEnvWithDefault("TEST_ENABLED", false, mustBeTrue)
	if result != false {
		t.Errorf("expected default false, got %v", result)
	}
}

// TestGetEnvWithDefault_Float64Validation tests validation with float64 type
func TestGetEnvWithDefault_Float64Validation(t *testing.T) {
	os.Setenv("TEST_RATIO", "0.5")
	defer os.Unsetenv("TEST_RATIO")

	inRange := func(v float64) bool { return v >= 0.0 && v <= 1.0 }

	result := GetEnvWithDefault("TEST_RATIO", 0.8, inRange)
	if result != 0.5 {
		t.Errorf("expected 0.5, got %v", result)
	}

	// Test out of range
	os.Setenv("TEST_RATIO", "1.5")
	result = GetEnvWithDefault("TEST_RATIO", 0.8, inRange)
	if result != 0.8 {
		t.Errorf("expected default 0.8, got %v", result)
	}
}

// TestGetEnvWithDefault_DurationValidation tests validation with time.Duration type
func TestGetEnvWithDefault_DurationValidation(t *testing.T) {
	os.Setenv("TEST_TIMEOUT", "30s")
	defer os.Unsetenv("TEST_TIMEOUT")

	isPositive := func(v time.Duration) bool { return v > 0 }
	lessThanMinute := func(v time.Duration) bool { return v < time.Minute }

	result := GetEnvWithDefault("TEST_TIMEOUT", 10*time.Second, isPositive, lessThanMinute)
	if result != 30*time.Second {
		t.Errorf("expected 30s, got %v", result)
	}

	// Test out of range
	os.Setenv("TEST_TIMEOUT", "2m")
	result = GetEnvWithDefault("TEST_TIMEOUT", 10*time.Second, isPositive, lessThanMinute)
	if result != 10*time.Second {
		t.Errorf("expected default 10s, got %v", result)
	}
}
