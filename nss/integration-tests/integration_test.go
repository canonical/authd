package nss_test

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/internal/users/db"
	"github.com/stretchr/testify/require"
)

var daemonPath string

func TestIntegration(t *testing.T) {
	t.Parallel()

	// codeNotFound is the expected exit code for the getent subprocess in case of errors.
	const codeNotFound int = 2

	libPath, rustCovEnv := testutils.BuildRustNSSLib(t, false, "should_pre_check_env")

	// Create a default daemon to use for most test cases.
	defaultSocket := filepath.Join(os.TempDir(), "nss-integration-tests.sock")
	defaultDbState := "multiple_users_and_groups"
	defaultGroupsFilePath := filepath.Join(filepath.Join("testdata", "empty.group"))

	testutils.StartAuthd(t, daemonPath,
		testutils.WithSocketPath(defaultSocket),
		testutils.WithPreviousDBState(defaultDbState),
		testutils.WithGroupFile(defaultGroupsFilePath),
		testutils.WithCurrentUserAsRoot,
	)

	tests := map[string]struct {
		getentDB string
		key      string
		dbState  string

		noDaemon       bool
		wantSecondCall bool
		shouldPreCheck bool

		wantStatus int
	}{
		"Get_all_entries_from_passwd": {getentDB: "passwd"},
		"Get_all_entries_from_group":  {getentDB: "group"},
		"Get_all_entries_from_shadow": {getentDB: "shadow"},

		"Get_entry_from_passwd_by_name":               {getentDB: "passwd", key: "user1@example.com"},
		"Get_entry_from_passwd_by_name_in_upper_case": {getentDB: "passwd", key: "USER1@EXAMPLE.COM"},
		"Get_entry_from_group_by_name":                {getentDB: "group", key: "group1"},
		"Get_entry_from_shadow_by_name":               {getentDB: "shadow", key: "user1@example.com"},

		"Get_entry_from_passwd_by_id": {getentDB: "passwd", key: "1111"},
		"Get_entry_from_group_by_id":  {getentDB: "group", key: "11111"},

		"Check_user_with_broker_if_not_found_in_db":               {getentDB: "passwd", key: "user-pre-check@example.com", shouldPreCheck: true},
		"Check_user_with_broker_if_not_found_in_db_in_upper_case": {getentDB: "passwd", key: strings.ToUpper("user-pre-check@example.com"), shouldPreCheck: true},

		// Even though those are "error" cases, the getent command won't fail when trying to list content of a service.
		"Returns_empty_when_getting_all_entries_from_passwd_and_daemon_is_not_available": {getentDB: "passwd", noDaemon: true},
		"Returns_empty_when_getting_all_entries_from_group_and_daemon_is_not_available":  {getentDB: "group", noDaemon: true},
		"Returns_empty_when_getting_all_entries_from_shadow_and_daemon_is_not_available": {getentDB: "shadow", noDaemon: true},

		/* Error cases */
		"Error_when_getting_passwd_by_name_and_entry_does_not_exist":                        {getentDB: "passwd", key: "doesnotexit", wantStatus: codeNotFound},
		"Error_when_getting_passwd_by_name_entry_exists_in_broker_but_precheck_is_disabled": {getentDB: "passwd", key: "user-pre-check@example.com", wantStatus: codeNotFound},
		"Error_when_getting_group_by_name_and_entry_does_not_exist":                         {getentDB: "group", key: "doesnotexit", wantStatus: codeNotFound},
		"Error_when_getting_shadow_by_name_and_entry_does_not_exist":                        {getentDB: "shadow", key: "doesnotexit", wantStatus: codeNotFound},

		"Error_when_getting_passwd_by_id_and_entry_does_not_exist": {getentDB: "passwd", key: "404", wantStatus: codeNotFound},
		"Error_when_getting_group_by_id_and_entry_does_not_exist":  {getentDB: "group", key: "404", wantStatus: codeNotFound},

		"Error_when_getting_passwd_by_name_and_daemon_is_not_available": {getentDB: "passwd", key: "user1@example.com", noDaemon: true, wantStatus: codeNotFound},
		"Error_when_getting_group_by_name_and_daemon_is_not_available":  {getentDB: "group", key: "group1", noDaemon: true, wantStatus: codeNotFound},
		"Error_when_getting_shadow_by_name_and_daemon_is_not_available": {getentDB: "shadow", key: "user1@example.com", noDaemon: true, wantStatus: codeNotFound},

		"Error_when_getting_passwd_by_id_and_daemon_is_not_available": {getentDB: "passwd", key: "1111", noDaemon: true, wantStatus: codeNotFound},
		"Error_when_getting_group_by_id_and_daemon_is_not_available":  {getentDB: "group", key: "11111", noDaemon: true, wantStatus: codeNotFound},

		/* Special cases */
		"Do_not_query_the_db_when_user_is_pam_unix_non_existent": {getentDB: "passwd", key: "pam_unix_non_existent:", dbState: "pam_unix_non_existent", wantStatus: codeNotFound},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			socketPath := defaultSocket

			var useAlternativeDaemon bool
			if tc.dbState != "" {
				useAlternativeDaemon = true
			} else {
				tc.dbState = defaultDbState
			}

			// We don't check compatibility of arguments, have noDaemon taking precedences to the others.
			if tc.noDaemon {
				socketPath = "/non-existent"
				useAlternativeDaemon = false
			}

			if useAlternativeDaemon {
				// Run a specific new daemon for special test cases.
				socketPath = testutils.StartAuthd(t, daemonPath,
					testutils.WithPreviousDBState(tc.dbState),
					testutils.WithGroupFile(defaultGroupsFilePath),
				)
			}

			cmds := []string{tc.getentDB}
			if tc.key != "" {
				cmds = append(cmds, tc.key)
			}

			got, status := getentOutputForLib(t, libPath, socketPath, rustCovEnv, tc.shouldPreCheck, cmds...)
			require.Equal(t, tc.wantStatus, status, "Expected status %d, but got %d", tc.wantStatus, status)

			if tc.shouldPreCheck && tc.getentDB == "passwd" {
				// When pre-checking, the `getent passwd` output contains a randomly generated UID.
				// To make the test deterministic, we replace the UID and GID with a placeholder.
				// The output looks something like this:
				//     user-pre-check:x:1776689191:1776689191:gecos for user-pre-check:/home/user-pre-check:/usr/bin/bash\n
				fields := strings.Split(got, ":")
				require.Len(t, fields, 7, "Invalid number of fields in the output: %q", got)
				// The UID is the third field.
				fields[2] = "{{UID}}"
				// The GID is the fourth field.
				fields[3] = "{{GID}}"
				got = strings.Join(fields, ":")
			}

			// If the exit status is NotFound, there is no need to create an empty golden file.
			// But we need to ensure that the output is indeed empty.
			if tc.wantStatus == codeNotFound {
				require.Empty(t, got, "Expected empty output, but got %q", got)
				return
			}

			golden.CheckOrUpdate(t, got)

			// This is to check that some cache tasks, such as cleaning a corrupted database, work as expected.
			if tc.wantSecondCall {
				got, status := getentOutputForLib(t, libPath, socketPath, rustCovEnv, tc.shouldPreCheck, cmds...)
				require.NotEqual(t, codeNotFound, status, "Expected no error, but got %v", status)
				require.Empty(t, got, "Expected empty output, but got %q", got)
			}
		})
	}
}

func TestProviderAliasSupplementaryGroups(t *testing.T) {
	t.Parallel()

	const unixName = "user1@example.com"
	const providerName = "renamed-user1@example.com"
	libPath, rustCovEnv := testutils.BuildRustNSSLib(t, false)
	dbDir := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.Mkdir(dbDir, 0700))
	fixture, err := os.ReadFile(filepath.Join("testdata", "db", "multiple_users_and_groups.db.yaml"))
	require.NoError(t, err)
	content := strings.Replace(string(fixture), "    - name: "+unixName+"\n",
		"    - name: "+unixName+"\n      provider_username: "+providerName+"\n", 1)
	require.NoError(t, db.Z_ForTests_CreateDBFromYAMLReader(strings.NewReader(content), dbDir))

	groupFile := filepath.Join(t.TempDir(), "group")
	require.NoError(t, os.WriteFile(groupFile, []byte("localgroup:x:45000:"+unixName+"\n"), 0600))
	socket := testutils.StartAuthd(t, daemonPath, testutils.WithDBPath(dbDir),
		testutils.WithGroupFile(groupFile), testutils.WithCurrentUserAsRoot)
	unixEntry, status := getentOutputForLib(t, libPath, socket, rustCovEnv, false, "passwd", unixName)
	require.Zero(t, status)
	aliasEntry, status := getentOutputForLib(t, libPath, socket, rustCovEnv, false, "passwd", providerName)
	require.Zero(t, status)
	require.Equal(t, unixEntry, aliasEntry)
	require.True(t, strings.HasPrefix(aliasEntry, unixName+":"))

	nsswitch := filepath.Join(filepath.Dir(groupFile), "nsswitch.conf")
	require.NoError(t, os.WriteFile(nsswitch, []byte("passwd: authd\ngroup: files authd\n"), 0600))
	runLookup := func(database, name string) string {
		t.Helper()
		env := append(os.Environ(), rustCovEnv...)
		env = append(env, "AUTHD_NSS_SOCKET="+socket, "LD_LIBRARY_PATH="+filepath.Dir(libPath))
		cmd := testutils.BubbleWrapCommand(t, env)
		cmd.Args = append(cmd.Args,
			"--ro-bind", filepath.Dir(groupFile), "/etc",
			"--ro-bind", filepath.Dir(socket), filepath.Dir(socket),
			"--ro-bind", filepath.Dir(libPath), filepath.Dir(libPath),
			"/usr/bin/getent", database, name,
		)
		var output strings.Builder
		cmd.Stdout = &output
		require.NoError(t, cmd.Run())
		return strings.TrimSpace(output.String())
	}
	require.Equal(t, "localgroup:x:45000:"+unixName, runLookup("group", "localgroup"))
	require.Equal(t, "commongroup::99999:user1@example.com,user2@example.com,user3@example.com",
		runLookup("group", "commongroup"))
	for _, name := range []string{unixName, providerName} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			groups := strings.Fields(runLookup("initgroups", name))
			require.NotEmpty(t, groups)
			require.Equal(t, name, groups[0])
			if name == unixName {
				require.ElementsMatch(t, []string{"11111", "45000", "99999"}, groups[1:])
				return
			}
			// glibc's fallback compares the requested name with canonical group members.
			require.Empty(t, groups[1:])
		})
	}
}

func TestMain(m *testing.M) {
	var cleanup func()
	var err error
	daemonPath, cleanup, err = testutils.BuildAuthdWithExampleBroker()
	if err != nil {
		log.Printf("Setup: failed to build daemon: %v", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
