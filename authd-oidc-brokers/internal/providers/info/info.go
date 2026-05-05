// Package info defines types used by the broker.
package info

import (
	"github.com/mitchellh/mapstructure"
)

// Group represents the group information that is fetched by the broker.
type Group struct {
	Name string `json:"name"`
	UGID string `json:"ugid"`
}

// User represents the user information obtained from the provider.
type User struct {
	Name   string  `json:"name"`
	UUID   string  `json:"uuid"`
	Home   string  `json:"dir"`
	Shell  string  `json:"shell"`
	Gecos  string  `json:"gecos"`
	Groups []Group `json:"groups"`
}

// NewUser creates a new user with the specified values.
//
// It fills the defaults for Shell and Gecos if they are empty.
func NewUser(name, home, uuid, shell, gecos string, groups []Group) User {
	u := User{
		Name:   name,
		Home:   home,
		UUID:   uuid,
		Shell:  shell,
		Gecos:  gecos,
		Groups: groups,
	}

	if u.Home == "" {
		u.Home = u.Name
	}
	if u.Shell == "" {
		u.Shell = "/usr/bin/bash"
	}
	if u.Gecos == "" {
		u.Gecos = u.Name
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
