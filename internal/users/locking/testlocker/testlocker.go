// TiCS: disabled // This is a test helper.
//go:build test_locker

package main

import (
	"log"
	"time"

	userslocking "github.com/canonical/authd/internal/users/locking"
)

func main() {
	log.Println("Locking database...")
	err := userslocking.WriteLock()
	if err != nil {
		log.Fatal(err)
	}

	<-time.After(999999 * time.Hour)
}
