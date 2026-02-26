package localentries_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/authd/internal/users/localentries"
	"github.com/canonical/authd/internal/users/types"
	"github.com/stretchr/testify/require"
)

func TestParseLocalPasswdFile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		passwdLines []string

		wantEntries []types.UserEntry
		wantErr     bool
	}{
		"Valid_empty_file": {passwdLines: []string{}},
		"Valid_single_entry": {
			passwdLines: []string{
				"testuser:x:1000:1000:Test User:/home/testuser:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "testuser",
					UID:   1000,
					GID:   1000,
					Gecos: "Test User",
					Dir:   "/home/testuser",
					Shell: "/bin/bash",
				},
			},
		},
		"Multiple_valid_entries": {
			passwdLines: []string{
				"user1:x:1001:1001:User One:/home/user1:/bin/sh",
				"user2:x:1002:1002:User Two:/home/user2:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "user1",
					UID:   1001,
					GID:   1001,
					Gecos: "User One",
					Dir:   "/home/user1",
					Shell: "/bin/sh",
				},
				{
					Name:  "user2",
					UID:   1002,
					GID:   1002,
					Gecos: "User Two",
					Dir:   "/home/user2",
					Shell: "/bin/bash",
				},
			},
		},
		"Skip_comment_and_empty_lines": {
			passwdLines: []string{
				"",
				"# This is a comment",
				"user3:x:1003:1003:User Three:/home/user3:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "user3",
					UID:   1003,
					GID:   1003,
					Gecos: "User Three",
					Dir:   "/home/user3",
					Shell: "/bin/bash",
				},
			},
		},

		"Warn_if_invalid_entry_format_too_few_fields": {
			passwdLines: []string{
				"badentry:x:1004:1004:User Four:/home/user4", // only 6 fields
				"user4:x:1004:1004:User Four:/home/user4:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "user4",
					UID:   1004,
					GID:   1004,
					Gecos: "User Four",
					Dir:   "/home/user4",
					Shell: "/bin/bash",
				},
			},
		},
		"Warn_if_invalid_uid": {
			passwdLines: []string{
				"user5:x:notanuid:1005:User Five:/home/user5:/bin/bash",
				"user6:x:1006:1006:User Six:/home/user6:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "user6",
					UID:   1006,
					GID:   1006,
					Gecos: "User Six",
					Dir:   "/home/user6",
					Shell: "/bin/bash",
				},
			},
		},
		"Warn_if_invalid_gid": {
			passwdLines: []string{
				"user7:x:1007:notagid:User Seven:/home/user7:/bin/bash",
				"user8:x:1008:1008:User Eight:/home/user8:/bin/bash",
			},
			wantEntries: []types.UserEntry{
				{
					Name:  "user8",
					UID:   1008,
					GID:   1008,
					Gecos: "User Eight",
					Dir:   "/home/user8",
					Shell: "/bin/bash",
				},
			},
		},

		// Error cases.
		"Error_if_file_is_missing": {
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inputPasswdFilePath := filepath.Join(t.TempDir(), "passwd")

			if tc.passwdLines != nil {
				tc.passwdLines = append(tc.passwdLines, "")
				err := os.WriteFile(inputPasswdFilePath, []byte(strings.Join(tc.passwdLines, "\n")), 0600)
				require.NoError(t, err, "Setup: Failed to write passwd file to %s", inputPasswdFilePath)
			}

			le, entriesUnlock, err := localentries.WithUserDBLock(
				localentries.WithPasswdInputPath(inputPasswdFilePath),
				localentries.WithMockUserDBLocking(),
			)
			require.NoError(t, err, "Failed to lock the local entries")
			t.Cleanup(func() {
				err := entriesUnlock()
				require.NoError(t, err, "entriesUnlock should not fail to unlock the local entries")
			})

			got, err := le.GetLocalUserEntries()
			if tc.wantErr {
				require.Error(t, err, "parseLocalPasswdFile() returned no error")
			} else {
				require.NoError(t, err, "parseLocalPasswdFile() returned error")
			}

			require.Equal(t, tc.wantEntries, got, "entries")
		})
	}
}
