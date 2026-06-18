//go:build pam_debug

package main

import "fmt"

func init() {
	reportAuthtok = func(authtok string) {
		fmt.Printf("  PAM_AUTHTOK: %q\n", authtok)
	}
}
