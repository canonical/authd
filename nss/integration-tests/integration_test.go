package nss_test

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/examplebroker"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/internal/testutils/golden"
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

		"Get_entry_from_passwd_by_name":               {getentDB: "passwd", key: "user1"},
		"Get_entry_from_passwd_by_name_in_upper_case": {getentDB: "passwd", key: "USER1"},
		"Get_entry_from_group_by_name":                {getentDB: "group", key: "group1"},
		"Get_entry_from_shadow_by_name":               {getentDB: "shadow", key: "user1"},

		"Get_entry_from_passwd_by_id": {getentDB: "passwd", key: "1111"},
		"Get_entry_from_group_by_id":  {getentDB: "group", key: "11111"},

		"Check_user_with_broker_if_not_found_in_db":               {getentDB: "passwd", key: examplebroker.UserIntegrationPreCheckPrefix + "simple", shouldPreCheck: true},
		"Check_user_with_broker_if_not_found_in_db_in_upper_case": {getentDB: "passwd", key: strings.ToUpper(examplebroker.UserIntegrationPreCheckPrefix + "simple"), shouldPreCheck: true},

		// Even though those are "error" cases, the getent command won't fail when trying to list content of a service.
		"Returns_empty_when_getting_all_entries_from_passwd_and_daemon_is_not_available": {getentDB: "passwd", noDaemon: true},
		"Returns_empty_when_getting_all_entries_from_group_and_daemon_is_not_available":  {getentDB: "group", noDaemon: true},
		"Returns_empty_when_getting_all_entries_from_shadow_and_daemon_is_not_available": {getentDB: "shadow", noDaemon: true},

		/* Error cases */
		"Error_when_getting_passwd_by_name_and_entry_does_not_exist":                        {getentDB: "passwd", key: "doesnotexit", wantStatus: codeNotFound},
		"Error_when_getting_passwd_by_name_entry_exists_in_broker_but_precheck_is_disabled": {getentDB: "passwd", key: examplebroker.UserIntegrationPreCheckPrefix + "simple", wantStatus: codeNotFound},
		"Error_when_getting_group_by_name_and_entry_does_not_exist":                         {getentDB: "group", key: "doesnotexit", wantStatus: codeNotFound},
		"Error_when_getting_shadow_by_name_and_entry_does_not_exist":                        {getentDB: "shadow", key: "doesnotexit", wantStatus: codeNotFound},

		"Error_when_getting_passwd_by_id_and_entry_does_not_exist": {getentDB: "passwd", key: "404", wantStatus: codeNotFound},
		"Error_when_getting_group_by_id_and_entry_does_not_exist":  {getentDB: "group", key: "404", wantStatus: codeNotFound},

		"Error_when_getting_passwd_by_name_and_daemon_is_not_available": {getentDB: "passwd", key: "user1", noDaemon: true, wantStatus: codeNotFound},
		"Error_when_getting_group_by_name_and_daemon_is_not_available":  {getentDB: "group", key: "group1", noDaemon: true, wantStatus: codeNotFound},
		"Error_when_getting_shadow_by_name_and_daemon_is_not_available": {getentDB: "shadow", key: "user1", noDaemon: true, wantStatus: codeNotFound},

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
