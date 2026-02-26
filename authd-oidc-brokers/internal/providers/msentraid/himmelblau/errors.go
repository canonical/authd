package himmelblau

import "fmt"

// ErrDeviceDisabled is returned when the device is disabled in Microsoft Entra ID.
var ErrDeviceDisabled = fmt.Errorf("device is disabled in Microsoft Entra ID")

// ErrInvalidRedirectURI is returned when the redirect URI of the client application is missing or invalid.
var ErrInvalidRedirectURI = fmt.Errorf("invalid redirect URI")

// TokenAcquisitionError is returned when an error occurs while acquiring a token via libhimmelblau.
type TokenAcquisitionError struct {
	msg string
}

func (e TokenAcquisitionError) Error() string {
	return e.msg
}
