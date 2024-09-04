package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/testutils"
)

var configTypes = map[string]string{
	"valid": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id
`,

	"valid+optional": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
home_base_dir = /home
allowed_ssh_suffixes = @issuer.url.com
`,

	"singles": `
[oidc]
issuer = https://ISSUER_URL>
client_id = <CLIENT_ID
`,

	"template": `
[oidc]
issuer = https://<ISSUER_URL>
client_id = <CLIENT_ID>
`,
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configType string

		wantErr bool
	}{
		"Successfully parse config file":                      {},
		"Successfully parse config file with optional values": {configType: "valid+optional"},

		"Do not fail if values contain a single template delimiter": {configType: "singles"},

		"Error if file does not exist": {configType: "inexistent", wantErr: true},
		"Error if file is unreadable":  {configType: "unreadable", wantErr: true},
		"Error if file is not updated": {configType: "template", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			confPath := filepath.Join(t.TempDir(), "broker.conf")

			if tc.configType == "" {
				tc.configType = "valid"
			}
			err := os.WriteFile(confPath, []byte(configTypes[tc.configType]), 0600)
			require.NoError(t, err, "Setup: Failed to write config file")

			switch tc.configType {
			case "inexistent":
				err = os.Remove(confPath)
				require.NoError(t, err, "Setup: Failed to remove config file")
			case "unreadable":
				err = os.Chmod(confPath, 0000)
				require.NoError(t, err, "Setup: Failed to make config file unreadable")
			}

			got, err := parseConfig(confPath)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			want := testutils.LoadWithUpdateFromGoldenYAML(t, got)
			require.EqualValues(t, want, got)
		})
	}
}
