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
  }
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

func runApplyEntraUnixIDs(t *testing.T, input string, apply bool, extraEnv ...string) (output, calls string, runErr error) {
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
	args := []string{"--authctl", authctlPath, tokenPath}
	if apply {
		args = append([]string{"--apply"}, args...)
	}
	// #nosec:G204 - The script and arguments are controlled by this test.
	cmd := exec.Command("bash", append([]string{"apply-entra-unix-ids"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AUTHCTL_LOG="+logPath, "REAL_JQ="+jqPath, "TOKEN_PATH="+tokenPath, "READ_LOG="+readLogPath)
	cmd.Env = append(cmd.Env, extraEnv...)
	result, runErr := cmd.CombinedOutput()
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	reads, err := os.ReadFile(readLogPath)
	require.NoError(t, err)
	require.Equal(t, tokenPath+"\n", string(reads), "the cache must be opened exactly once")
	return string(result), string(log), runErr
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

	input := `{"UserInfo":{"name":"Alice","uid":41001,"groups":[{"name":"ALICE","ugid":"private-group-id","gid":41001}]}}`
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

	const token = `{"UserInfo":{"name":"alice","uid":2147483647,"groups":[{"name":"engineering","ugid":"group-engineering","gid":2147483646}]}}`
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
{"name":"staff","ugid":"second","gid":42002}]}}`
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
	const input = `{"UserInfo":{"name":"alice","uid":41001,"groups":[{"name":"engineering","ugid":"first","gid":42001}]}}`
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
			input:     `{"UserInfo":{"name":"alice","uid":41001}}`,
			wantCalls: "user set-uid alice 41001\ngroup set-gid alice 41001\n",
		},
		{
			name:      "remote GID only leaves private group unchanged",
			input:     `{"UserInfo":{"name":"alice","groups":[{"name":"engineering","ugid":"first","gid":42001}]}}`,
			wantCalls: "group set-gid engineering 42001\n",
		},
		{
			name:      "null UID leaves private group unchanged",
			input:     `{"UserInfo":{"name":"alice","uid":null,"groups":[{"name":"engineering","ugid":"first","gid":42001}]}}`,
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
