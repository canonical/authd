//go:build withmsentraid

package msentraid

import (
	"errors"
	"fmt"
	"math"
)

// The systemd dynamic user and group ID range. authd never allocates IDs in
// this range, so IDs from Entra must not use it either.
const (
	systemdDynamicIDMin = 61184
	systemdDynamicIDMax = 65519
)

// parseUnixID converts a value read from a Graph directory extension property
// into a Unix ID.
//
// The Graph SDK stores every JSON number of a property it does not know as a
// *float64, and a String or Boolean typed property as a *string or *bool, so
// those are the shapes that reach this function. An unset value arrives as a
// missing key, which parseConfiguredUnixID handles before calling this.
func parseUnixID(value any) (*uint32, error) {
	switch value := value.(type) {
	case *float64:
		if value == nil {
			return nil, nil
		}
		return validateUnixID(*value)
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

// validateUnixID checks that a value can be a Unix ID. Directory extension
// properties hold signed 32-bit integers, so anything that is not a positive
// integer in that range is rejected.
func validateUnixID(value float64) (*uint32, error) {
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

	parsed := uint32(value)
	if parsed == 0 {
		return nil, errors.New("value must be positive")
	}
	if parsed == 65534 || parsed == 65535 {
		return nil, fmt.Errorf("value %d is reserved", parsed)
	}
	if parsed >= systemdDynamicIDMin && parsed <= systemdDynamicIDMax {
		return nil, fmt.Errorf("value %d is reserved for systemd dynamic users and groups", parsed)
	}
	return &parsed, nil
}
