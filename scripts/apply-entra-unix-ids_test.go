package scripts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyEntraUnixIDsSkipsLocalMappings(t *testing.T) {
	t.Parallel()

	const token = `{
  "UserInfo": {
    "name": "alice@example.com",
    "uid": 41001,
    "groups": [
      {"name": "linux-sudo", "ugid": "", "gid": 49001},
      {"name": "engineering", "ugid": "group-engineering", "gid": 42001},
      {"name": "without-gid", "ugid": "group-without-gid", "gid": null}
    ]
  },
  "UnixAttributeNames": {"UID": "extension_uidNumber", "GID": "extension_gidNumber"}
}`
	output, calls, err := runApplyEntraUnixIDs(t, token, true)
	require.NoError(t, err, "apply-entra-unix-ids failed: %s", output)
	require.Contains(t, output, "user set-uid alice@example.com 41001")
	require.Contains(t, output, "group set-gid alice@example.com 41001")
	require.Contains(t, output, "group set-gid engineering 42001")
	require.NotContains(t, output, "linux-sudo")
	require.NotContains(t, output, "without-gid")
	require.Equal(t,
		"user set-uid alice@example.com 41001\n"+
			"group set-gid alice@example.com 41001\n"+
			"group set-gid engineering 42001\n",
		calls,
	)
}

// defaultAttributeArgs returns the attribute options that match the names in the test fixtures.
func defaultAttributeArgs() []string {
	return []string{"--uid-attribute", "extension_uidNumber", "--gid-attribute", "extension_gidNumber"}
}

// applyRun holds the observable results of one script run.
type applyRun struct {
	output    string
	calls     string
	reads     string
	tokenPath string
	err       error
}

// runApplyEntraUnixIDs runs the script with the default attribute names and requires one cache read.
func runApplyEntraUnixIDs(t *testing.T, input string, apply bool, extraEnv ...string) (output, calls string, runErr error) {
	t.Helper()

	run := runApplyEntraUnixIDsWithArgs(t, input, apply, defaultAttributeArgs(), extraEnv...)
	require.Equal(t, run.tokenPath+"\n", run.reads, "the cache must be opened exactly once")
	return run.output, run.calls, run.err
}

// runApplyEntraUnixIDsWithArgs runs the script with args and returns what it did.
func runApplyEntraUnixIDsWithArgs(t *testing.T, input string, apply bool, args []string, extraEnv ...string) applyRun {
	t.Helper()

	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is required to run apply-entra-unix-ids")
	}
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	require.NoError(t, os.WriteFile(tokenPath, []byte(input), 0600))
	logPath := filepath.Join(dir, "authctl.log")
	readLogPath := filepath.Join(dir, "reads.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0600))
	require.NoError(t, os.WriteFile(readLogPath, nil, 0600))
	authctlPath := filepath.Join(dir, "authctl")
	// #nosec:G306 - This mock must be executable and is private to the test directory.
	require.NoError(t, os.WriteFile(authctlPath, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$AUTHCTL_LOG"
if [ "$3" = "${AUTHCTL_FAIL_TARGET:-}" ] && { [ -z "${AUTHCTL_FAIL_KIND:-}" ] || [ "$1" = "$AUTHCTL_FAIL_KIND" ]; }; then
    exit 1
fi
`), 0700))
	jqWrapperPath := filepath.Join(dir, "jq")
	// #nosec:G306 - This mock must be executable and is private to the test directory.
	require.NoError(t, os.WriteFile(jqWrapperPath, []byte(`#!/bin/sh
reads_token=false
for argument do
    if [ "$argument" = "$TOKEN_PATH" ]; then
        reads_token=true
        printf '%s\n' "$argument" >> "$READ_LOG"
    fi
done
"$REAL_JQ" "$@" || exit $?
if [ "$reads_token" = true ] && [ -n "${REPLACEMENT_TOKEN:-}" ]; then
    printf '%s' "$REPLACEMENT_TOKEN" > "$TOKEN_PATH"
fi
`), 0700))
	var scriptArgs []string
	if apply {
		scriptArgs = append(scriptArgs, "--apply")
	}
	scriptArgs = append(scriptArgs, args...)
	scriptArgs = append(scriptArgs, "--authctl", authctlPath, tokenPath)
	// #nosec:G204 - The script and arguments are controlled by this test.
	cmd := exec.Command("bash", append([]string{"apply-entra-unix-ids"}, scriptArgs...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AUTHCTL_LOG="+logPath, "REAL_JQ="+jqPath, "TOKEN_PATH="+tokenPath, "READ_LOG="+readLogPath)
	cmd.Env = append(cmd.Env, extraEnv...)
	result, runErr := cmd.CombinedOutput()
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	reads, err := os.ReadFile(readLogPath)
	require.NoError(t, err)
	return applyRun{output: string(result), calls: string(log), reads: string(reads), tokenPath: tokenPath, err: runErr}
}

func TestApplyEntraUnixIDsRejectsInvalidInputBeforeApplying(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"malformed JSON":      `{"UserInfo":`,
		"multiple documents":  `{"UserInfo":{"name":"alice","uid":41001}} {"UserInfo":{"name":"bob","uid":41002}}`,
		"empty input":         ``,
		"missing user":        `{}`,
		"empty username":      `{"UserInfo":{"name":"","uid":41001}}`,
		"groups not an array": `{"UserInfo":{"name":"alice","uid":41001,"groups":{}}}`,
		"false groups":        `{"UserInfo":{"name":"alice","uid":41001,"groups":false}}`,
		"group not an object": `{"UserInfo":{"name":"alice","uid":41001,"groups":[null]}}`,
		"missing group name":  `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"ugid":"first","gid":42001}]}}`,
		"invalid group id":    `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":123,"gid":42001}]}}`,
		"same identity different GIDs": `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"engineering","ugid":"first","gid":42001},{"name":"engineering","ugid":"first","gid":42002}]}}`,
		"same identity different names": `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"engineering","ugid":"first","gid":42001},{"name":"staff","ugid":"first","gid":42001}]}}`,
		"same normalized name different GIDs": `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"Engineering","ugid":"first","gid":42001},{"name":"engineering","ugid":"second","gid":42002}]}}`,
		"same normalized name different identities": `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"Engineering","ugid":"first","gid":42001},{"name":"engineering","ugid":"second","gid":42001}]}}`,
		"same GID different identities": `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"engineering","ugid":"first","gid":42001},{"name":"staff","ugid":"second","gid":42001}]}}`,
		"UID conflicts with remote group GID": `{"UserInfo":{"name":"alice","uid":42001,"groups":[{"name":"engineering","ugid":"first","gid":42001}]}}`,
		"names not an object":                 `{"UserInfo":{"name":"alice","uid":41001},"UnixAttributeNames":"extension_uidNumber"}`,
		"UID name not a string":               `{"UserInfo":{"name":"alice","uid":41001},"UnixAttributeNames":{"UID":1,"GID":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			output, calls, err := runApplyEntraUnixIDs(t, input, true)
			require.Error(t, err, output)
			require.Empty(t, calls, "invalid input must not invoke authctl")
		})
	}
}

func TestApplyEntraUnixIDsRejectsPrivateGroupGIDConflictBeforeApplying(t *testing.T) {
	t.Parallel()

	input := `{"UserInfo":{"name":"Alice","uid":41001,"groups":[{"name":"ALICE","ugid":"private-group-id","gid":42001}]}}`
	output, calls, err := runApplyEntraUnixIDs(t, input, true)
	require.Error(t, err, output)
	require.Empty(t, calls, "conflicting private-group GID must be rejected before invoking authctl")
	require.Contains(t, output, "private group Alice GID must match cached UID 41001, but remote group alice is assigned GID 42001")
}

func TestApplyEntraUnixIDsAllowsPrivateGroupGIDMatchingUID(t *testing.T) {
	t.Parallel()

	input := `{"UserInfo":{"name":"Alice","uid":41001,"groups":[{"name":"ALICE","ugid":"private-group-id","gid":41001}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	output, calls, err := runApplyEntraUnixIDs(t, input, true)
	require.NoError(t, err, output)
	require.Equal(t,
		"user set-uid Alice 41001\n"+
			"group set-gid Alice 41001\n"+
			"group set-gid alice 41001\n",
		calls,
	)
}

func TestApplyEntraUnixIDsRejectsInvalidNumbers(t *testing.T) {
	t.Parallel()

	for _, value := range []string{`"41001"`, `true`, `-1`, `0`, `1.5`, `65534`, `65535`, `2147483648`, `4294967295`, `1e100`, `18446744073709551617`} {
		for _, field := range []string{"uid", "gid"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				t.Parallel()
				input := fmt.Sprintf(`{"UserInfo":{"name":"alice","uid":%s}}`, value)
				if field == "gid" {
					input = fmt.Sprintf(`{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":"group-id","gid":%s}]}}`, value)
				}
				output, calls, err := runApplyEntraUnixIDs(t, input, true)
				require.Error(t, err, output)
				require.Empty(t, calls)
			})
		}
	}
}

func TestApplyEntraUnixIDsAcceptsMaximumSigned32BitIDs(t *testing.T) {
	t.Parallel()

	const token = `{"UserInfo":{"name":"alice","uid":2147483647,"groups":[{"name":"engineering","ugid":"group-engineering","gid":2147483646}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	output, calls, err := runApplyEntraUnixIDs(t, token, true)
	require.NoError(t, err, output)
	require.Equal(t,
		"user set-uid alice 2147483647\n"+
			"group set-gid alice 2147483647\n"+
			"group set-gid engineering 2147483646\n",
		calls,
	)
}

func TestApplyEntraUnixIDsPlanAndFailures(t *testing.T) {
	t.Parallel()
	const input = `{"UserInfo":{"name":"alice","uid":41001,"groups":[
{"name":"Engineering","ugid":"first","gid":42001},
{"name":"engineering","ugid":"first","gid":42001},
{"name":"staff","ugid":"second","gid":42002}]},
"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	for _, tc := range []struct {
		name      string
		apply     bool
		failName  string
		failKind  string
		wantCalls string
	}{
		{name: "dry run"},
		{name: "deduplicate and normalize", apply: true, wantCalls: "user set-uid alice 41001\ngroup set-gid alice 41001\ngroup set-gid engineering 42001\ngroup set-gid staff 42002\n"},
		{name: "stop after UID failure", apply: true, failName: "alice", wantCalls: "user set-uid alice 41001\n"},
		{name: "stop after private GID failure", apply: true, failName: "alice", failKind: "group", wantCalls: "user set-uid alice 41001\ngroup set-gid alice 41001\n"},
		{name: "stop after GID failure", apply: true, failName: "engineering", wantCalls: "user set-uid alice 41001\ngroup set-gid alice 41001\ngroup set-gid engineering 42001\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output, calls, err := runApplyEntraUnixIDs(t, input, tc.apply,
				"AUTHCTL_FAIL_TARGET="+tc.failName, "AUTHCTL_FAIL_KIND="+tc.failKind)
			if tc.failName != "" {
				require.Error(t, err, output)
				require.Contains(t, output, "not rolled back")
				if tc.failKind == "group" {
					require.Contains(t, output, "authctl group set-gid failed for private group alice")
				}
			} else {
				require.NoError(t, err, output)
			}
			require.Equal(t, tc.wantCalls, calls)
			require.Contains(t, output, "group set-gid alice 41001")
			require.Contains(t, output, "group set-gid engineering 42001")
		})
	}
}

func TestApplyEntraUnixIDsUsesOneSnapshot(t *testing.T) {
	t.Parallel()
	const input = `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":"first","gid":42001}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	const replacement = `{"UserInfo":{"name":"bob","uid":51001,"groups":[{"name":"staff","ugid":"second","gid":52001}]}}`
	output, calls, err := runApplyEntraUnixIDs(t, input, true, "REPLACEMENT_TOKEN="+replacement)
	require.NoError(t, err, output)
	require.Equal(t, "user set-uid alice 41001\ngroup set-gid alice 41001\ngroup set-gid engineering 42001\n", calls)
}

func TestApplyEntraUnixIDsPartialAssignments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		input     string
		wantCalls string
	}{
		{
			name:      "UID only updates private group",
			input:     `{"UserInfo":{"name":"alice","uid":41001},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`,
			wantCalls: "user set-uid alice 41001\ngroup set-gid alice 41001\n",
		},
		{
			name:      "remote GID only leaves private group unchanged",
			input:     `{"UserInfo":{"name":"alice","groups":[{"name":"engineering","ugid":"first","gid":42001}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`,
			wantCalls: "group set-gid engineering 42001\n",
		},
		{
			name:      "null UID leaves private group unchanged",
			input:     `{"UserInfo":{"name":"alice","uid":null,"groups":[{"name":"engineering","ugid":"first","gid":42001}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`,
			wantCalls: "group set-gid engineering 42001\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output, calls, err := runApplyEntraUnixIDs(t, tc.input, true)
			require.NoError(t, err, output)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestApplyEntraUnixIDsAcceptsMissingOptionalFields(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"UserInfo":{"name":"alice"}}`,
		`{"UserInfo":{"name":"alice","uid":null,"groups":null}}`,
		`{"UserInfo":{"name":"alice","groups":[{"name":"sudo"},{"name":"staff","ugid":"first"}]}}`,
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			output, calls, err := runApplyEntraUnixIDs(t, input, true)
			require.NoError(t, err, output)
			require.Empty(t, calls)
			require.Contains(t, output, "No cached Unix UID or remote group GID assignments found")
		})
	}
}

func TestApplyEntraUnixIDsRefusesCacheWithOtherAttributeNames(t *testing.T) {
	t.Parallel()

	const cached = `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":"group-engineering","gid":42001}]},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	const withoutNames = `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":"group-engineering","gid":42001}]}}`
	for _, tc := range []struct {
		name        string
		input       string
		args        []string
		wantMessage string
	}{
		{
			name:        "UID attribute changed",
			input:       cached,
			args:        []string{"--uid-attribute", "extension_newUidNumber"},
			wantMessage: "read from UID attribute 'extension_uidNumber', but the configured UID attribute is 'extension_newUidNumber'",
		},
		{
			name:        "GID attribute changed",
			input:       cached,
			args:        []string{"--gid-attribute", "extension_newGidNumber"},
			wantMessage: "read from GID attribute 'extension_gidNumber', but the configured GID attribute is 'extension_newGidNumber'",
		},
		{
			name:        "attribute names missing",
			input:       withoutNames,
			wantMessage: "have no attribute names",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := runApplyEntraUnixIDsWithArgs(t, tc.input, true, append(defaultAttributeArgs(), tc.args...))
			require.Error(t, run.err, run.output)
			require.Contains(t, run.output, tc.wantMessage)
			require.Empty(t, run.calls, "a refused cache must not invoke authctl")
			require.Equal(t, run.tokenPath+"\n", run.reads, "the cache must be opened exactly once")
		})
	}
}

func TestApplyEntraUnixIDsIgnoresAttributeNamesWithoutCachedIDs(t *testing.T) {
	t.Parallel()

	const input = `{"UserInfo":{"name":"alice","uid":null,"groups":[{"name":"linux-sudo","ugid":"","gid":49001}]},"UnixAttributeNames":{"UID":"extension_oldUidNumber","GID":"extension_oldGidNumber"}}`
	output, calls, err := runApplyEntraUnixIDs(t, input, true)
	require.NoError(t, err, output)
	require.Empty(t, calls)
	require.Contains(t, output, "No cached Unix UID or remote group GID assignments found")
}

func TestApplyEntraUnixIDsRequiresAttributeNameOptions(t *testing.T) {
	t.Parallel()

	const input = `{"UserInfo":{"name":"alice","uid":41001},"UnixAttributeNames":{"UID":"extension_uidNumber","GID":"extension_gidNumber"}}`
	for name, args := range map[string][]string{
		"no options":         nil,
		"only UID attribute": {"--uid-attribute", "extension_uidNumber"},
		"only GID attribute": {"--gid-attribute", "extension_gidNumber"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := runApplyEntraUnixIDsWithArgs(t, input, true, args)
			require.Error(t, run.err, run.output)
			require.Contains(t, run.output, "--uid-attribute and --gid-attribute are required")
			require.Empty(t, run.reads, "the cache must not be opened without the attribute names")
			require.Empty(t, run.calls, "missing options must not invoke authctl")
		})
	}
}

func TestApplyEntraUnixIDsResolvesShortAttributeNames(t *testing.T) {
	t.Parallel()

	const input = `{"UserInfo":{"name":"alice","uid":41001},"UnixAttributeNames":{"UID":"extension_abc123_Linux_UID","GID":"extension_abc123_Linux_GID"}}`
	t.Run("with client ID", func(t *testing.T) {
		t.Parallel()

		run := runApplyEntraUnixIDsWithArgs(t, input, true, []string{"--uid-attribute", "Linux_UID", "--gid-attribute", "Linux_GID", "--client-id", "abc-123"})
		require.NoError(t, run.err, run.output)
		require.Equal(t, "user set-uid alice 41001\ngroup set-gid alice 41001\n", run.calls)
		require.Equal(t, run.tokenPath+"\n", run.reads, "the cache must be opened exactly once")
	})
	t.Run("without client ID", func(t *testing.T) {
		t.Parallel()

		run := runApplyEntraUnixIDsWithArgs(t, input, true, []string{"--uid-attribute", "Linux_UID", "--gid-attribute", "Linux_GID"})
		require.Error(t, run.err, run.output)
		require.Contains(t, run.output, "a short attribute name needs --client-id: Linux_UID")
		require.Empty(t, run.calls, "an unresolved name must not invoke authctl")
	})
}
