package himmelblau

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidDeviceRegistrationDataJSON(t *testing.T) {
	t.Parallel()

	const validJSON = `{
		"device_id":       "00000000-0000-0000-0000-000000000001",
		"cert_key":        "AQID",
		"transport_key":   "BAUG",
		"auth_value":      "auth",
		"tpm_machine_key": "BwgJ"
	}`

	tests := map[string]struct {
		raw  []byte
		want bool
	}{
		"Valid":                   {raw: []byte(validJSON), want: true},
		"Nil":                     {raw: nil, want: false},
		"Empty":                   {raw: []byte(""), want: false},
		"Not_JSON":                {raw: []byte("not-json"), want: false},
		"Empty_object":            {raw: []byte("{}"), want: false},
		"Missing_device_id":       {raw: []byte(`{"cert_key":"AQID","transport_key":"BAUG","auth_value":"x","tpm_machine_key":"BwgJ"}`), want: false},
		"Empty_device_id":         {raw: []byte(`{"device_id":"","cert_key":"AQID","transport_key":"BAUG","auth_value":"x","tpm_machine_key":"BwgJ"}`), want: false},
		"Empty_cert_key":          {raw: []byte(`{"device_id":"d","cert_key":"","transport_key":"BAUG","auth_value":"x","tpm_machine_key":"BwgJ"}`), want: false},
		"Empty_transport_key":     {raw: []byte(`{"device_id":"d","cert_key":"AQID","transport_key":"","auth_value":"x","tpm_machine_key":"BwgJ"}`), want: false},
		"Empty_auth_value":        {raw: []byte(`{"device_id":"d","cert_key":"AQID","transport_key":"BAUG","auth_value":"","tpm_machine_key":"BwgJ"}`), want: false},
		"Empty_tpm_machine_key":   {raw: []byte(`{"device_id":"d","cert_key":"AQID","transport_key":"BAUG","auth_value":"x","tpm_machine_key":""}`), want: false},
		"Wrong_type_for_cert_key": {raw: []byte(`{"device_id":"d","cert_key":42,"transport_key":"BAUG","auth_value":"x","tpm_machine_key":"BwgJ"}`), want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidDeviceRegistrationDataJSON(tc.raw))
		})
	}
}
