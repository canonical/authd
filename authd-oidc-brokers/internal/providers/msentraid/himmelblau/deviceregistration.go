package himmelblau

import "encoding/json"

// DeviceRegistrationData contains the data returned by RegisterDevice which is
// needed to acquire an access token later. The fields are populated by the
// libhimmelblau-backed flow but the struct is defined here (without a build
// tag) so that the broker can validate cached JSON without depending on the
// libhimmelblau build.
type DeviceRegistrationData struct {
	DeviceID      string `json:"device_id"`
	CertKey       []byte `json:"cert_key"`
	TransportKey  []byte `json:"transport_key"`
	AuthValue     string `json:"auth_value"`
	TPMMachineKey []byte `json:"tpm_machine_key"`
}

// IsValid checks whether all fields of the DeviceRegistrationData are set.
func (d *DeviceRegistrationData) IsValid() bool {
	return d.DeviceID != "" &&
		len(d.CertKey) > 0 &&
		len(d.TransportKey) > 0 &&
		d.AuthValue != "" &&
		len(d.TPMMachineKey) > 0
}

// ValidDeviceRegistrationDataJSON reports whether raw is a valid JSON-encoded
// device registration payload with all required fields present and non-empty.
func ValidDeviceRegistrationDataJSON(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var d DeviceRegistrationData
	if err := json.Unmarshal(raw, &d); err != nil {
		return false
	}
	return d.IsValid()
}
