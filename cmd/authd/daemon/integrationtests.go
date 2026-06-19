//go:build integrationtests

package daemon

import (
	"fmt"
	"os"

	"github.com/canonical/authd/internal/testsdetection"
)

// load any behaviour modifiers from env variable.
func init() {
	testsdetection.MustBeTesting()

	if ocd := os.Getenv("AUTHD_INTEGRATIONTESTS_OLD_DB_DIR"); ocd != "" {
		oldDBDir = ocd
		fmt.Printf("Using old DB directory %q\n", oldDBDir)
	}
}
