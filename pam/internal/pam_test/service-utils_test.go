package pam_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateService(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		services    []ServiceLine
		wantContent string
	}{
		"empty":         {},
		"CApital-Empty": {},
		"auth-sufficient-permit": {
			services: []ServiceLine{
				{Auth, Sufficient, Permit.String(), []string{}},
			},
			wantContent: "auth	sufficient	pam_permit.so",
		},
		"auth-sufficient-permit-args": {
			services: []ServiceLine{
				{Auth, Required, Deny.String(), []string{"a b c [d e]"}},
			},
			wantContent: "auth	required	pam_deny.so	[a b c [d e\\]]",
		},
		"account-sufficient-requisite": {
			services: []ServiceLine{
				{Auth, SufficientRequisite, Permit.String(), []string{}},
			},
			wantContent: "auth	[success=done new_authtok_reqd=done ignore=ignore default=die]	pam_permit.so",
		},
		"complete-custom": {
			services: []ServiceLine{
				{Account, Required, "pam_account_module.so", []string{"a", "b", "c", "d e", "f [g h]"}},
				{Account, Required, Deny.String(), []string{}},
				{Auth, Requisite, "pam_auth_module.so", []string{}},
				{Auth, Requisite, Deny.String(), []string{}},
				{Include, Control(0), "common-auth", []string{}},
				{Auth, NewControl("[success=done ignore=1]"), Permit.String(), []string{}},
				{Auth, NewControl("[success=die ignore=done]"), Permit.String(), []string{}},
				{Auth, NewControl("[success=done ignore=1]"), Deny.String(), []string{}},
				{Password, Sufficient, "pam_password_module.so", []string{"arg"}},
				{Password, Sufficient, Deny.String(), []string{}},
				{Session, Optional, "pam_session_module.so", []string{""}},
				{Session, Optional, Ignore.String(), []string{}},
			},
			wantContent: `account	required	pam_account_module.so	a b c [d e] [f [g h\]]
account	required	pam_deny.so
auth	requisite	pam_auth_module.so
auth	requisite	pam_deny.so
@include		common-auth
auth	[success=done ignore=1]	pam_permit.so
auth	[success=die ignore=done]	pam_permit.so
auth	[success=done ignore=1]	pam_deny.so
password	sufficient	pam_password_module.so	arg
password	sufficient	pam_deny.so
session	optional	pam_session_module.so
session	optional	pam_debug.so auth=incomplete cred=incomplete acct=incomplete prechauthtok=incomplete chauthtok=incomplete open_session=incomplete close_session=incomplete`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service, err := CreateService(t.TempDir(), name, tc.services)
			require.NoError(t, err, "Can't create service file")
			require.Equal(t, strings.ToLower(name), filepath.Base(service),
				"Invalid service name %s", service)

			bytes, err := os.ReadFile(service)
			require.NoError(t, err, "Failed to read service file")
			require.Equal(t, tc.wantContent, string(bytes),
				"unexpected service file %s content", service)
		})
	}
}

func TestCreateServiceError(t *testing.T) {
	t.Parallel()

	service, err := CreateService("/no-Existent", "invalid-path", []ServiceLine{})
	require.Empty(t, service, "Service path is not empty")
	require.ErrorIs(t, err, os.ErrNotExist)
}
