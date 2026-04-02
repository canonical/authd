package main_test

import (
	"log"
	"os"
	"testing"

	"github.com/canonical/authd/internal/testutils"
)

var daemonPath string

func TestMain(m *testing.M) {
	var cleanup func()
	var err error
	daemonPath, cleanup, err = testutils.BuildAuthdWithExampleBroker()
	if err != nil {
		log.Printf("Setup: failed to build daemon: %v", err)
		os.Exit(1)
	}
	defer cleanup()

	m.Run()
}
