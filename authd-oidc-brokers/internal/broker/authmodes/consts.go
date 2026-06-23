// Package authmodes lists the authentication modes that providers can support.
package authmodes

const (
	// Password is the ID of the password authentication method.
	Password = "password"

	// Device is the ID of the device authentication method.
	Device = "device_auth"

	// DeviceQr is the ID of the device authentication method when QrCode rendering is enabled.
	DeviceQr = "device_auth_qr"

	// NewPassword is the ID of the new password configuration method.
	NewPassword = "newpassword"

	// EntraPassword is the ID of the Entra ID password + MFA authentication method.
	EntraPassword = "entra_password"

	// EntraMFAWait is the ID of the poll-based MFA follow-up mode.
	EntraMFAWait = "entra_mfa_wait"

	// EntraMFACode is the ID of the code-entry MFA follow-up mode.
	EntraMFACode = "entra_mfa_code"
)

var (
	// Label is a map of auth mode IDs to their display labels.
	//nolint:gosec // G101: These are auth mode display labels, not credentials.
	Label = map[string]string{
		Password:      "Local password",
		Device:        "Device Authentication",
		DeviceQr:      "Device Authentication",
		NewPassword:   "Define your local password",
		EntraPassword: "Entra ID password",
		EntraMFAWait:  "Waiting for MFA approval",
		EntraMFACode:  "Enter your MFA code",
	}
)
