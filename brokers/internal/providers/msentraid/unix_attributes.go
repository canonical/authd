//go:build withmsentraid

package msentraid

import (
	"fmt"
	"math"
)

func parseUnixID(value any) (*uint32, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case float64:
		return validateUnixIDFloat(value)
	case *float64:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDFloat(*value)
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func validateUnixIDUnsigned(value uint64) (*uint32, error) {
	if value == 0 {
		return nil, fmt.Errorf("value must be positive")
	}
	if value == 65534 || value == 65535 || value == math.MaxUint32 {
		return nil, fmt.Errorf("value %d is reserved", value)
	}
	if value > math.MaxInt32 {
		return nil, fmt.Errorf("value %d is greater than the signed 32-bit maximum", value)
	}
	parsed := uint32(value)
	return &parsed, nil
}

func validateUnixIDFloat(value float64) (*uint32, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("value %v is not finite", value)
	}
	if math.Trunc(value) != value {
		return nil, fmt.Errorf("value %v is fractional", value)
	}
	if value < 0 {
		return nil, fmt.Errorf("value %v is negative", value)
	}
	// Check before converting: out-of-range float-to-integer results are implementation-dependent.
	if value > math.MaxInt32 {
		return nil, fmt.Errorf("value %v is greater than the signed 32-bit maximum", value)
	}
	return validateUnixIDUnsigned(uint64(value))
}
