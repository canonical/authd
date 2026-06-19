// TiCS: disabled // Should only be built when running integration tests.

package main

import (
	"fmt"
	"os"

	"github.com/canonical/authd/internal/testsdetection"
)

func main() {
	defer func() {
		// Catch the panic so that we can get the coverage from it.
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Panic: %v\n", r)
			os.Exit(2)
		}
	}()
	testsdetection.MustBeTesting()
}
