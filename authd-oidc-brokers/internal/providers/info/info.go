// Package info defines types used by the broker.
package info

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
