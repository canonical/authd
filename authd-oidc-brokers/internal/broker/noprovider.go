//go:build !withmsentraid

// This is the generic oidc extension. It’s a no-op for most of the calls.

package broker

import "fmt"

func getGroups() {
	fmt.Println("No provider getGroups")
}
