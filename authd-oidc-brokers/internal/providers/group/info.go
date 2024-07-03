// Package group defines group-related types used by the broker.
package group

// Info represents the group information that is fetched by the broker.
type Info struct {
	Name string `json:"name"`
	UGID string `json:"ugid"`
}
