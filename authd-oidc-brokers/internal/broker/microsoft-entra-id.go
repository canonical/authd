//go:build withmsentraid

// This is the Microsoft Entra ID specific extension.

package broker

import "fmt"

func getGroups() {
	fmt.Println("Microsoft Entra ID getGroups")
}
