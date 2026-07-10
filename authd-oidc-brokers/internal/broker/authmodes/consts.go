// Package authmodes lists the authentication modes that providers can support.
package authmodes

const (
	// Password is the ID of the password authentication flow.
	Password = "password"

	// Device is the ID of the device code flow.
	// The ID value remains "device_auth" for compatibility.
	Device = "device_auth"

	// DeviceQr is the ID of the device code flow when QrCode rendering is enabled.
	// The ID value remains "device_auth_qr" for compatibility.
	DeviceQr = "device_auth_qr"

	// NewPassword is the ID of the new password configuration method.
	NewPassword = "newpassword"

	// EntraAuth is the ID of the Entra ID password/passwordless authentication method.
	EntraAuth = "entra_auth"

	// EntraAuthWait is the ID of the poll-based MFA follow-up mode.
	EntraAuthWait = "entra_auth_wait"

	// EntraAuthCode is the ID of the code-entry MFA follow-up mode.
	EntraAuthCode = "entra_auth_code"

	// EntraAuthFido is the ID of the security-key MFA follow-up mode, which
	// performs the WebAuthn assertion with a locally connected FIDO2 device.
	EntraAuthFido = "entra_auth_fido"

	// EntraAuthFidoPin is the ID of the security-key PIN entry mode, chained
	// before EntraAuthFido when the connected device requires a client PIN.
	EntraAuthFidoPin = "entra_auth_fido_pin"
)

var (
	// Label is a map of auth mode IDs to their display labels.
	Label = map[string]string{
		Password:         "Local password",
		Device:           "Device code flow",
		DeviceQr:         "Device code flow",
		NewPassword:      "Define your local password",
		EntraAuth:        "Entra ID authentication",
		EntraAuthWait:    "Waiting for MFA approval",
		EntraAuthCode:    "Enter your MFA code",
		EntraAuthFido:    "Use your security key",
		EntraAuthFidoPin: "Enter your security key PIN",
	}
)
