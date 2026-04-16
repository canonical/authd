package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
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
force_access_check_with_provider = true
extra_scopes = groups,offline_access, some_other_scope

[users]
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,

	"invalid_boolean_value": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id
force_access_check_with_provider = invalid
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

	"overwrite_lower_precedence": `
[oidc]
issuer = https://lower-precedence-issuer.url.com
client_id = lower_precedence_client_id
`,

	"overwrite_higher_precedence": `
[oidc]
issuer = https://higher-precedence-issuer.url.com
`,
}

func TestParseConfig(t *testing.T) {
	t.Parallel()
	p := &testutils.MockProvider{}
	ignoredFields := map[string]struct{}{"provider": {}, "ownerMutex": {}}

	tests := map[string]struct {
		configType string
		dropInType string

		wantErr bool
	}{
		"Successfully_parse_config_file":                      {},
		"Successfully_parse_config_file_with_optional_values": {configType: "valid+optional"},
		"Successfully_parse_config_with_drop_in_files":        {dropInType: "valid"},

		"Do_not_fail_if_values_contain_a_single_template_delimiter": {configType: "singles"},

		"Error_if_file_does_not_exist":             {configType: "inexistent", wantErr: true},
		"Error_if_file_is_unreadable":              {configType: "unreadable", wantErr: true},
		"Error_if_file_is_not_updated":             {configType: "template", wantErr: true},
		"Error_if_drop_in_directory_is_unreadable": {dropInType: "unreadable-dir", wantErr: true},
		"Error_if_drop_in_file_is_unreadable":      {dropInType: "unreadable-file", wantErr: true},
		"Error_if_config_contains_invalid_values":  {configType: "invalid_boolean_value", wantErr: true},
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

			dropInDir := GetDropInDir(confPath)
			if tc.dropInType != "" {
				err = os.Mkdir(dropInDir, 0700)
				require.NoError(t, err, "Setup: Failed to create drop-in directory")
			}

			switch tc.dropInType {
			case "valid":
				// Create multiple drop-in files to test that they are loaded in the correct order.
				err = os.WriteFile(filepath.Join(dropInDir, "00-drop-in.conf"), []byte(configTypes["overwrite_lower_precedence"]), 0600)
				require.NoError(t, err, "Setup: Failed to write drop-in file")
				err = os.WriteFile(filepath.Join(dropInDir, "01-drop-in.conf"), []byte(configTypes["overwrite_higher_precedence"]), 0600)
				require.NoError(t, err, "Setup: Failed to write drop-in file")
				// Create the main config file, to test that the options which are not overwritten by the drop-in files
				// are still present.
				err = os.WriteFile(confPath, []byte(configTypes["valid+optional"]), 0600)
				require.NoError(t, err, "Setup: Failed to write config file")
			case "unreadable-dir":
				err = os.Chmod(dropInDir, 0000)
				require.NoError(t, err, "Setup: Failed to make drop-in directory unreadable")
			case "unreadable-file":
				err = os.WriteFile(filepath.Join(dropInDir, "00-drop-in.conf"), []byte(configTypes["valid"]), 0000)
				require.NoError(t, err, "Setup: Failed to make drop-in file unreadable")
			}

			cfg, err := parseConfigFromPath(confPath, p)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, cfg.provider, p)

			outDir := t.TempDir()
			// Write the names and values of all fields in the config to a file. We can't use the json or yaml
			// packages because they can't access unexported fields.
			var fields []string
			val := reflect.ValueOf(&cfg).Elem()
			typ := reflect.TypeOf(&cfg).Elem()
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if _, ok := ignoredFields[field.Name]; ok {
					continue
				}
				fieldValue := val.Field(i)
				if field.PkgPath != "" {
					//nolint: gosec // We are using unsafe to access unexported fields for testing purposes
					fieldValue = reflect.NewAt(fieldValue.Type(), unsafe.Pointer(fieldValue.UnsafeAddr())).Elem()
				}
				fields = append(fields, fmt.Sprintf("%s=%v", field.Name, fieldValue))
			}
			err = os.WriteFile(filepath.Join(outDir, "config.txt"), []byte(strings.Join(fields, "\n")), 0600)
			require.NoError(t, err)

			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

var testParseUserConfigTypes = map[string]string{
	"All_are_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = ALL
owner = machine_owner
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Only_owner_is_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = OWNER
owner = machine_owner
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"By_default_only_owner_is_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
owner = machine_owner
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Only_owner_is_allowed_but_is_unset": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Only_owner_is_allowed_but_is_empty": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
owner =
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Users_u1_and_u2_are_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = u1,u2
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Unset_owner_and_u1_is_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = OWNER,u1
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Set_owner_and_u1_is_allowed": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = OWNER,u1
owner = machine_owner
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
	"Support_old_suffixes_key": `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
allowed_users = ALL
owner = machine_owner
home_base_dir = /home
ssh_allowed_suffixes_first_auth = @issuer.url.com
`,
}

func TestParseUserConfig(t *testing.T) {
	t.Parallel()
	p := &testutils.MockProvider{}

	tests := map[string]struct {
		wantAllUsersAllowed       bool
		wantOwnerAllowed          bool
		wantFirstUserBecomesOwner bool
		wantOwner                 string
		wantAllowedUsers          []string
	}{
		"All_are_allowed":                    {wantAllUsersAllowed: true, wantOwner: "machine_owner"},
		"Only_owner_is_allowed":              {wantOwnerAllowed: true, wantOwner: "machine_owner"},
		"By_default_only_owner_is_allowed":   {wantOwnerAllowed: true, wantOwner: "machine_owner"},
		"Only_owner_is_allowed_but_is_unset": {wantOwnerAllowed: true, wantFirstUserBecomesOwner: true},
		"Only_owner_is_allowed_but_is_empty": {wantOwnerAllowed: true},
		"Users_u1_and_u2_are_allowed":        {wantAllowedUsers: []string{"u1", "u2"}},
		"Unset_owner_and_u1_is_allowed": {
			wantOwnerAllowed:          true,
			wantFirstUserBecomesOwner: true,
			wantAllowedUsers:          []string{"u1"},
		},
		"Set_owner_and_u1_is_allowed": {
			wantOwnerAllowed: true,
			wantOwner:        "machine_owner",
			wantAllowedUsers: []string{"u1"},
		},
		"Support_old_suffixes_key": {wantAllUsersAllowed: true, wantOwner: "machine_owner"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			confPath := filepath.Join(outDir, "broker.conf")

			err := os.WriteFile(confPath, []byte(testParseUserConfigTypes[name]), 0600)
			require.NoError(t, err, "Setup: Failed to write config file")

			dropInDir := GetDropInDir(confPath)
			err = os.Mkdir(dropInDir, 0700)
			require.NoError(t, err, "Setup: Failed to create drop-in directory")

			cfg, err := parseConfigFromPath(confPath, p)

			// convert the allowed users array to a map
			allowedUsersMap := map[string]struct{}{}
			for _, k := range tc.wantAllowedUsers {
				allowedUsersMap[k] = struct{}{}
			}

			require.Equal(t, tc.wantAllUsersAllowed, cfg.allUsersAllowed)
			require.Equal(t, tc.wantOwnerAllowed, cfg.ownerAllowed)
			require.Equal(t, tc.wantOwner, cfg.owner)
			require.Equal(t, tc.wantFirstUserBecomesOwner, cfg.firstUserBecomesOwner)
			require.Equal(t, allowedUsersMap, cfg.allowedUsers)

			require.NoError(t, err)
			golden.CheckOrUpdateFileTree(t, outDir)
		})
	}
}

func TestRegisterOwner(t *testing.T) {
	p := &testutils.MockProvider{}
	outDir := t.TempDir()
	userName := "owner_name"
	confPath := filepath.Join(outDir, "broker.conf")

	err := os.WriteFile(confPath, []byte(configTypes["valid"]), 0600)
	require.NoError(t, err, "Setup: Failed to write config file")

	dropInDir := GetDropInDir(confPath)
	err = os.Mkdir(dropInDir, 0700)
	require.NoError(t, err, "Setup: Failed to create drop-in directory")

	cfg := userConfig{firstUserBecomesOwner: true, ownerAllowed: true, provider: p, ownerMutex: &sync.RWMutex{}}
	err = cfg.registerOwner(confPath, userName)
	require.NoError(t, err)

	require.Equal(t, cfg.owner, userName)
	require.Equal(t, cfg.firstUserBecomesOwner, false)

	f, err := os.Open(filepath.Join(dropInDir, "20-owner-autoregistration.conf"))
	require.NoError(t, err, "failed to open 20-owner-autoregistration.conf")
	defer f.Close()

	golden.CheckOrUpdateFileTree(t, outDir)
}

func TestParseConfigUnknownSettings(t *testing.T) {
	// This test is NOT parallel because it modifies the global log handler.
	p := &testutils.MockProvider{}

	tests := map[string]struct {
		config      string
		wantWarning string
	}{
		"No_warning_for_valid_config": {
			config: configTypes["valid"],
		},
		"No_warning_for_valid_config_with_optional_values": {
			config: configTypes["valid+optional"],
		},
		"No_warning_for_valid_config_with_old_force_provider_authentication_key": {
			config: `
[oidc]
issuer = https://issuer.url.com
client_id = client_id
force_provider_authentication = true
`,
		},
		"Warn_about_unknown_section": {
			config: `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[unknown_section]
some_key = some_value
`,
			wantWarning: `unknown section "unknown_section" in config file, ignoring`,
		},
		"Warn_about_unknown_key_in_oidc_section": {
			config: `
[oidc]
issuer = https://issuer.url.com
client_id = client_id
unknown_key = some_value
`,
			wantWarning: `unknown key "unknown_key" in section "oidc" in config file, ignoring`,
		},
		"Warn_about_unknown_key_in_users_section": {
			config: `
[oidc]
issuer = https://issuer.url.com
client_id = client_id

[users]
unknown_key = some_value
`,
			wantWarning: `unknown key "unknown_key" in section "users" in config file, ignoring`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var capturedWarnings []string
			log.SetLevelHandler(log.WarnLevel, func(_ context.Context, _ log.Level, format string, args ...interface{}) {
				capturedWarnings = append(capturedWarnings, fmt.Sprintf(format, args...))
			})
			t.Cleanup(func() {
				log.SetLevelHandler(log.WarnLevel, nil)
			})

			confPath := filepath.Join(t.TempDir(), "broker.conf")
			err := os.WriteFile(confPath, []byte(tc.config), 0600)
			require.NoError(t, err, "Setup: Failed to write config file")

			_, err = parseConfigFromPath(confPath, p)
			require.NoError(t, err)

			if tc.wantWarning == "" {
				require.Empty(t, capturedWarnings, "No warnings should have been produced")
			} else {
				require.Contains(t, capturedWarnings, tc.wantWarning)
			}
		})
	}
}

func TestBrokerConfFilesHaveNoUnknownSettings(t *testing.T) {
	// This test is NOT parallel because it modifies the global log handler.
	p := &testutils.MockProvider{}

	confFiles := map[string]string{
		"google":    "../../conf/variants/google/broker.conf",
		"msentraid": "../../conf/variants/msentraid/broker.conf",
		"oidc":      "../../conf/variants/oidc/broker.conf",
	}

	for name, confFile := range confFiles {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(confFile)
			require.NoError(t, err, "Setup: Failed to read broker.conf")

			// Replace template placeholders with valid dummy values so that
			// parseConfig does not fail on the placeholder check.
			replaced := strings.NewReplacer(
				"<CLIENT_ID>", "test-client-id",
				"<CLIENT_SECRET>", "test-client-secret",
				"<ISSUER_URL>", "https://example.com",
				"<ISSUER_ID>", "test-issuer-id",
			).Replace(string(content))

			var capturedWarnings []string
			log.SetLevelHandler(log.WarnLevel, func(_ context.Context, _ log.Level, format string, args ...interface{}) {
				capturedWarnings = append(capturedWarnings, fmt.Sprintf(format, args...))
			})
			t.Cleanup(func() {
				log.SetLevelHandler(log.WarnLevel, nil)
			})

			confPath := filepath.Join(t.TempDir(), "broker.conf")
			err = os.WriteFile(confPath, []byte(replaced), 0600)
			require.NoError(t, err, "Setup: Failed to write config file")

			_, err = parseConfigFromPath(confPath, p)
			require.NoError(t, err)

			require.Empty(t, capturedWarnings, "No unknown-setting warnings should be produced for the %s broker.conf", name)
		})
	}
}

func FuzzParseConfig(f *testing.F) {
	p := &testutils.MockProvider{}
	f.Fuzz(func(t *testing.T, a []byte) {
		_, _ = parseConfig(a, nil, p)
	})
}
