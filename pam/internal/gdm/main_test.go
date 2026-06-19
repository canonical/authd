package gdm

import (
	"os"
	"testing"

	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/pam_test"
)

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	exit := m.Run()
	pam_test.MaybeDoLeakCheck()
	os.Exit(exit)
}
