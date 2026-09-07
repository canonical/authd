package gdm

import (
	"os"
	"testing"

	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/pam_test"
)

var defaultExtensions = []string{PamExtensionCustomJSON}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)
	AdvertisePamExtensions(defaultExtensions)

	exit := m.Run()

	AdvertisePamExtensions(nil)
	pam_test.MaybeDoLeakCheck()
	os.Exit(exit)
}
