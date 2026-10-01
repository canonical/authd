//go:build withmsentraid

package msentraid

import (
	"encoding/json"
	"fmt"
	"math"
)

func parseUnixID(value any) (*uint32, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case int:
		return validateUnixID(int64(value))
	case int8:
		return validateUnixID(int64(value))
	case int16:
		return validateUnixID(int64(value))
	case int32:
		return validateUnixID(int64(value))
	case int64:
		return validateUnixID(value)
	case *int:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(int64(*value))
	case *int8:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(int64(*value))
	case *int16:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(int64(*value))
	case *int32:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(int64(*value))
	case *int64:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(*value)
	case uint:
		return validateUnixIDUnsigned(uint64(value))
	case uint8:
		return validateUnixIDUnsigned(uint64(value))
	case uint16:
		return validateUnixIDUnsigned(uint64(value))
	case uint32:
		return validateUnixIDUnsigned(uint64(value))
	case uint64:
		return validateUnixIDUnsigned(value)
	case *uint:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDUnsigned(uint64(*value))
	case *uint8:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDUnsigned(uint64(*value))
	case *uint16:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDUnsigned(uint64(*value))
	case *uint32:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDUnsigned(uint64(*value))
	case *uint64:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDUnsigned(*value)
	case float32:
		return validateUnixIDFloat(float64(value))
	case float64:
		return validateUnixIDFloat(value)
	case *float32:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDFloat(float64(*value))
	case *float64:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDFloat(*value)
	case json.Number:
		return validateUnixIDNumber(value)
	case *json.Number:
		if value == nil {
			return nil, nil
		}
		return validateUnixIDNumber(*value)
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func validateUnixID(value int64) (*uint32, error) {
	if value < 0 {
		return nil, fmt.Errorf("value %d is negative", value)
	}
	return validateUnixIDUnsigned(uint64(value))
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
	return validateUnixIDUnsigned(uint64(value))
}

func validateUnixIDNumber(value json.Number) (*uint32, error) {
	if integer, err := value.Int64(); err == nil {
		return validateUnixID(integer)
	}
	parsed, err := value.Float64()
	if err != nil {
		return nil, fmt.Errorf("invalid numeric value %q: %w", value, err)
	}
	return validateUnixIDFloat(parsed)
}
