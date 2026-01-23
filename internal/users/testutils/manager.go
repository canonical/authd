// Package userstestutils export db test functionalities used by other packages.
package userstestutils

import (
	"sync"
	"unsafe"

	"github.com/canonical/authd/internal/testsdetection"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/db"
)

func init() {
	// No import outside of testing environment.
	testsdetection.MustBeTesting()
}

type manager struct {
	userRegisterMu sync.Mutex

	db *db.Manager
}

// GetManagerDB returns the database of the manager.
func GetManagerDB(m *users.Manager) *db.Manager {
	//#nosec:G103 // This is only used in tests.
	mTest := *(*manager)(unsafe.Pointer(m))

	return mTest.db
}
