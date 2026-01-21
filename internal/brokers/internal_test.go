package brokers

import (
	"encoding/json"
	"testing"

	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
)

// These are used to test the JSON unmarshaling of the User struct.
var (
	completeJSON = `
{
	"Name":"success",
	"UID":82162,
	"Gecos":"gecos for success",
	"Dir":"/home/success",
	"Shell":"/bin/sh/success",
	"Groups":[
		{"Name":"success","GID":82162},
		{"Name":"group-success","GID":81868}
	]
}`
	emptyFieldJSON = `
{
	"Name":"",
	"UID":82162,
	"Gecos":"gecos for success",
	"Dir":"/home/success",
	"Shell":"/bin/sh/success",
	"Groups":[
		{"Name":"success","GID":82162},
		{"Name":"group-success","GID":81868}
	]
}`
	missingFieldJSON = `
{
	"UID":82162,
	"Gecos":"gecos for success",
	"Dir":"/home/success",
	"Shell":"/bin/sh/success",
	"Groups":[
		{"Name":"success","GID":82162},
		{"Name":"group-success","GID":81868}
	]
}`
	additionalFieldJSON = `
{
	"Name":"success",
	"UID":82162,
	"Gecos":"gecos for success",
	"Dir":"/home/success",
	"AdditionalFieldNotInStruct":"what's this?",
	"Shell":"/bin/sh/success",
	"Groups":[
		{"Name":"success","GID":82162},
		{"Name":"group-success","GID":81868}
	]
}`
)

func TestUnmarshalUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		jsonInput string

		wantErr bool
	}{
		"Successfully_unmarshal_complete_user_info":            {jsonInput: completeJSON},
		"Unmarshaling_json_with_empty_field_keeps_its_value":   {jsonInput: emptyFieldJSON},
		"Unmarshaling_json_with_missing_field_adds_zero_value": {jsonInput: missingFieldJSON},
		"Unmarshaling_json_with_additional_field_ignores_it":   {jsonInput: additionalFieldJSON},

		"Error_when_unmarshaling_invalid_json": {jsonInput: "invalid-json", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := unmarshalUserInfo([]byte(tc.jsonInput))
			if tc.wantErr {
				require.Error(t, err, "unmarshalUserInfo should return an error, but did not")
				return
			}
			require.NoError(t, err, "unmarshalUserInfo should not return an error, but did")

			gotJSON, err := json.Marshal(got)
			require.NoError(t, err, "Marshaling the result should not return an error, but did")

			golden.CheckOrUpdate(t, string(gotJSON))
		})
	}
}
