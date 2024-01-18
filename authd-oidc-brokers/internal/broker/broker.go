// Package broker is the generic oidc business code.
package broker

import (
	"fmt"
)

// Broker is the real implementation of the broker to track sessions and process oidc calls.
type Broker struct{}

// IsAuthenticated evaluates the provided authenticationData and returns the authentication status for the user.
func (b *Broker) IsAuthenticated(sessionID, authenticationData string) (access, data string, err error) {
	fmt.Println("IsAuthenticated generic OIDC code")
	getGroups()
	return "", "", nil
}
