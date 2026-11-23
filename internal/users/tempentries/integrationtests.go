//go:build integrationtests

package tempentries

import (
	"os"

	"github.com/canonical/authd/internal/systemd"
	"github.com/canonical/authd/internal/testsdetection"
)

func init() {
	testsdetection.MustBeTesting()
	if socket := os.Getenv("AUTHD_INTEGRATIONTESTS_DESCRIPTOR_STORE"); socket != "" {
		generationGuardPath = ""
		newDescriptorStore = func() (systemd.DescriptorStore, error) {
			return systemd.Z_ForTests_DescriptorStoreClient(socket), nil
		}
	}
}
