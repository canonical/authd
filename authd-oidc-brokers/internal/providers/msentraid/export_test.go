package msentraid

import "strings"

// AllExpectedScopes returns all the default expected scopes for a new provider.
func AllExpectedScopes() string {
	return strings.Join(New().expectedScopes, " ")
}
