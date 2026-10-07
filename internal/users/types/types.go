// Package types provides types for the users package.
package types

// UserInfo is the user information returned by the broker.
type UserInfo struct {
	Name  string
	UID   uint32
	Gecos string
	Dir   string
	Shell string

	// BrokerID is the provider scope for ProviderID. It is set by authd from the
	// selected broker, not by broker-returned JSON. It is always serialized (no
	// omitempty) because it scopes the user's identity.
	BrokerID string `json:"-" yaml:"broker_id"`

	// ProviderID is the stable OIDC subject identifier (or oid for MS Entra ID).
	// It is used as the primary matching key for returning users. Empty for
	// v2 brokers or users that have not yet logged in after the migration.
	ProviderID string `json:"provider_id,omitempty" yaml:"provider_id"`

	Groups []GroupInfo
}

// GroupInfo is the group information returned by the broker.
type GroupInfo struct {
	Name string
	GID  *uint32
	UGID string
}

// UserEntry is the user information sent to the NSS service.
type UserEntry struct {
	Name  string
	UID   uint32
	GID   uint32
	Gecos string
	Dir   string
	Shell string
}

// ShadowEntry is the shadow information sent to the NSS service.
type ShadowEntry struct {
	Name           string
	LastPwdChange  int
	MaxPwdAge      int
	PwdWarnPeriod  int
	PwdInactivity  int
	MinPwdAge      int
	ExpirationDate int
}

// GroupEntry is the group information sent to the NSS service.
type GroupEntry struct {
	Name   string
	GID    uint32
	Users  []string
	Passwd string
}
