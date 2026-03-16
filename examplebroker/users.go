package examplebroker

import "sync"

type userInfoBroker struct {
	Password string
}

var (
	exampleUsersMu = sync.RWMutex{}
	exampleUsers   = map[string]userInfoBroker{
		"user1@example.com":               {Password: "goodpass"},
		"user2@example.com":               {Password: "goodpass"},
		"user3@example.com":               {Password: "goodpass"},
		"user-ssh@example.com":            {Password: "goodpass"},
		"user-ssh2@example.com":           {Password: "goodpass"},
		"user-mfa@example.com":            {Password: "goodpass"},
		"user-mfa-with-reset@example.com": {Password: "goodpass"},
		"user-needs-reset@example.com":    {Password: "goodpass"},
		"user-needs-reset2@example.com":   {Password: "goodpass"},
		"user-can-reset@example.com":      {Password: "goodpass"},
		"user-can-reset2@example.com":     {Password: "goodpass"},
		"user-local-groups@example.com":   {Password: "goodpass"},
		"user-pre-check@example.com":      {Password: "goodpass"},
		"user-sudo@example.com":           {Password: "goodpass"},
	}
)

const (
	// UserIntegrationPrefix is the prefix for a user for integration tests.
	UserIntegrationPrefix = "user-integration-"
	// UserIntegrationMfaPrefix is the prefix for an mfa user for integration tests.
	UserIntegrationMfaPrefix = "user-mfa-integration-"
	// UserIntegrationMfaNeedsResetPrefix is the prefix for an mfa-needs-reset user for integration tests.
	UserIntegrationMfaNeedsResetPrefix = "user-mfa-needs-reset-integration-"
	// UserIntegrationMfaWithResetPrefix is the prefix for an mfa-with-reset user for integration tests.
	UserIntegrationMfaWithResetPrefix = "user-mfa-with-reset-integration-"
	// UserIntegrationNeedsResetPrefix is the prefix for a needs-reset user for integration tests.
	UserIntegrationNeedsResetPrefix = "user-needs-reset-integration-"
	// UserIntegrationCanResetPrefix is the prefix for a can-reset user for integration tests.
	UserIntegrationCanResetPrefix = "user-can-reset-integration-"
	// UserIntegrationLocalGroupsPrefix is the prefix for a local-groups user for integration tests.
	UserIntegrationLocalGroupsPrefix = "user-local-groups-integration-"
	// UserIntegrationQRcodeStaticPrefix is the prefix for a static qrcode user for integration tests.
	UserIntegrationQRcodeStaticPrefix = "user-integration-qrcode-static-"
	// UserIntegrationPreCheckValue is the value for a pre-check user for integration tests.
	UserIntegrationPreCheckValue = "pre-check"
	// UserIntegrationPreCheckPrefix is the prefix for a pre-check user for integration tests.
	UserIntegrationPreCheckPrefix = UserIntegrationPrefix + UserIntegrationPreCheckValue + "-"
	// UserIntegrationUnexistent is an unexistent user leading to a non-existent user error.
	UserIntegrationUnexistent = "user-unexistent@example.com"
	// UserIntegrationAuthModesPrefix is the prefix for a user listing for supported auth modes.
	// The modes can be exposed as list, in the form: `user-auth-modes-id1,id2,id3-integration-whatever`.
	UserIntegrationAuthModesPrefix = "user-auth-modes-"
)
