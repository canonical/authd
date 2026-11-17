// Package info defines types used by the broker.
package info

import (
	"context"
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"golang.org/x/oauth2"
)

// ErrUnixAttributeRequired marks an enrichment failure caused by a required
// Unix attribute being unavailable or invalid.
var (
	ErrUnixAttributeRequired = errors.New("required Unix attribute is unavailable")
	ErrUnixAttributeMissing  = errors.New("required Unix attribute is missing or invalid")
	ErrUnixUIDRequired       = fmt.Errorf("%w: required Unix UID is unavailable", ErrUnixAttributeRequired)
	ErrUnixGIDRequired       = fmt.Errorf("%w: required Unix GID is unavailable", ErrUnixAttributeRequired)
)

// ErrUnixAttributePartial comes with a usable user that keeps cached values for unreadable attributes.
var ErrUnixAttributePartial = errors.New("some Unix attributes could not be read")

// ErrGroupsUnavailable marks an enrichment that stopped before the groups were read.
var ErrGroupsUnavailable = errors.New("groups could not be read from the provider")

// Group represents the group information that is fetched by the broker.
type Group struct {
	Name string  `json:"name"`
	UGID string  `json:"ugid"`
	GID  *uint32 `json:"gid,omitempty" yaml:"gid,omitempty"`
	// AdditionalData is transient directory data used only during enrichment.
	AdditionalData map[string]any `json:"-" yaml:"-"`
}

// GroupFetchResult contains freshly fetched groups and any non-fatal error
// reading the configured Unix GID attribute. A GID attribute error means group
// membership was fetched successfully, but the attribute was not available.
type GroupFetchResult struct {
	Groups            []Group
	GIDAttributeError error
}

// User represents the user information obtained from the provider.
type User struct {
	Name string `json:"name"`
	// ProviderID is the stable OIDC subject identifier used to match returning users.
	// For MS Entra ID this is the oid claim (stable per tenant); for other
	// providers it is the standard sub claim.
	ProviderID string  `json:"provider_id"`
	Home       string  `json:"dir"`
	Shell      string  `json:"shell"`
	Gecos      string  `json:"gecos"`
	Groups     []Group `json:"groups"`
	UID        *uint32 `json:"uid,omitempty" yaml:"uid,omitempty"`
}

// UnixAttributeConfig controls optional Microsoft Entra Unix ID enrichment.
// Attribute names are already resolved to their full Graph property names by
// the broker configuration parser.
type UnixAttributeConfig struct {
	UIDAttribute string
	GIDAttribute string
	UIDRequired  bool
	GIDRequired  bool
}

// KeepCachedGIDs sets the GID of each group that has none to the cached GID of
// the group with the same UGID.
func KeepCachedGIDs(groups, cached []Group) {
	// The first cached GID of a UGID wins.
	cachedGIDs := make(map[string]uint32, len(cached))
	for _, c := range cached {
		if c.UGID == "" || c.GID == nil {
			continue
		}
		if _, ok := cachedGIDs[c.UGID]; !ok {
			cachedGIDs[c.UGID] = *c.GID
		}
	}

	for i := range groups {
		if groups[i].UGID == "" || groups[i].GID != nil {
			continue
		}
		if gid, ok := cachedGIDs[groups[i].UGID]; ok {
			groups[i].GID = &gid
		}
	}
}

// UnixAttributeEnricher enriches a user and already-fetched groups with
// directory-backed Unix IDs. It does not fetch group memberships.
// Unreadable attributes keep cached values and are reported with ErrUnixAttributePartial.
type UnixAttributeEnricher interface {
	EnrichUserWithUnixAttributes(
		ctx context.Context,
		user User,
		groups GroupFetchResult,
		clientID string,
		issuerURL string,
		token *oauth2.Token,
		providerMetadata map[string]interface{},
		deviceRegistrationData []byte,
		config UnixAttributeConfig,
	) (User, error)
}

// NewUser creates a new user with the specified values.
//
// It fills the defaults for Shell if it is empty.
func NewUser(name, home, providerID, shell, gecos string, groups []Group) User {
	u := User{
		Name:       name,
		Home:       home,
		ProviderID: providerID,
		Shell:      shell,
		Gecos:      gecos,
		Groups:     groups,
	}

	if u.Home == "" {
		u.Home = u.Name
	}
	if u.Shell == "" {
		u.Shell = "/usr/bin/bash"
	}

	return u
}

// Claimer is an interface that defines a method to extract the claims from the ID token.
type Claimer interface {
	Claims(any) error
}

// MergedClaimer is a Claimer that holds a merged map of claims from multiple sources.
// Claims from later Claimers override claims from earlier ones.
type MergedClaimer struct {
	merged map[string]interface{}
}

// NewMergedClaimer creates a MergedClaimer by merging claims from multiple Claimers.
// Claims from later Claimers override claims from earlier ones for the same key.
func NewMergedClaimer(claimers ...Claimer) (*MergedClaimer, error) {
	merged := make(map[string]interface{})
	for _, c := range claimers {
		var m map[string]interface{}
		if err := c.Claims(&m); err != nil {
			return nil, err
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	return &MergedClaimer{merged: merged}, nil
}

// Claims deserializes the merged claims into the provided value.
func (mc *MergedClaimer) Claims(v any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "json",
		Result:           v,
		ZeroFields:       true,
		ErrorUnused:      false,
		WeaklyTypedInput: false,
	})
	if err != nil {
		return err
	}
	return decoder.Decode(mc.merged)
}
