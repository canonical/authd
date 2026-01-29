// Package consts defines the constants used by the project.
package consts

import (
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ubuntu/authd/log"
)

var (
	// Version is the version of the executable.
	Version = "Dev"
)

const (
	// TEXTDOMAIN is the gettext domain for l10n.
	TEXTDOMAIN = "authd-oidc"

	// DefaultLevelLog is the default logging level selected without any option.
	DefaultLevelLog = log.WarnLevel

	// The application ID of the Microsoft-owned Azure Portal app.
	azurePortalAppID = "c44b4083-3bb0-49c1-b47d-974e53cbdf3c"
	// The ".default" scope for the Azure Portal app.
	azurePortalScope = azurePortalAppID + "/.default"

	// MicrosoftBrokerAppID is the application ID of the Microsoft-owned Microsoft Authentication Broker app.
	// This app is used in OAuth 2.0 authentication to acquire a token with the ".default" scope for the Azure Portal app.
	// That token can then be used to acquire a token for device registration.
	MicrosoftBrokerAppID = "29d9ed98-a469-4536-ade2-f981bc1d605e"
)

var (
	// MicrosoftBrokerAppScopes contains the OIDC scopes that we require for the Microsoft Authentication Broker app.
	// The ".default" scope for the Azure Portal app is needed to acquire a token for device registration.
	MicrosoftBrokerAppScopes = []string{oidc.ScopeOpenID, "profile", oidc.ScopeOfflineAccess, azurePortalScope}

	// DefaultScopes contains the OIDC scopes that we require for all providers.
	// Provider implementations can append additional scopes.
	DefaultScopes = []string{oidc.ScopeOpenID, "profile", "email"}
)
