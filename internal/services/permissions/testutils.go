package permissions

// All those functions and methods are only for tests.
// They are not exported, and guarded by testing assertions.

import (
	"fmt"
	"math"
	"os"
	"regexp"

	"github.com/canonical/authd/internal/testsdetection"
)

// uidInMsgRegex matches the uid field in gRPC peer credential messages (e.g. "uid: 0, pid: 123").
var uidInMsgRegex = regexp.MustCompile(`\buid: \d+`)

// Z_ForTests_WithCurrentUserAsRoot returns an Option that sets the rootUID to the current user's UID.
//
// nolint:revive,nolintlint // We want to use underscores in the function name here.
func Z_ForTests_WithCurrentUserAsRoot() Option {
	testsdetection.MustBeTesting()

	uid := currentUserUID()
	return func(o *options) {
		o.rootUID = uid
	}
}

// currentUserUID returns the current user UID or panics.
func currentUserUID() uint32 {
	testsdetection.MustBeTesting()

	uid := os.Geteuid()

	if uid < 0 || uint64(uid) > math.MaxUint32 {
		panic(fmt.Sprintf("current uid is not a valid uint32: %v", uid))
	}

	return uint32(uid)
}

// Z_ForTests_IdempotentPermissionError strips the UID from gRPC peer credential
// messages (format: "uid: <number>, pid: <number>") to make test output deterministic
// regardless of which user runs the tests.
//
// nolint:revive,nolintlint // We want to use underscores in the function name here.
func Z_ForTests_IdempotentPermissionError(msg string) string {
	testsdetection.MustBeTesting()

	return uidInMsgRegex.ReplaceAllString(msg, "uid: XXXX")
}

// Z_ForTests_DefaultCurrentUserAsRoot mocks the current user as root for the permission manager.
//
// nolint:revive,nolintlint // We want to use underscores in the function name here.
func Z_ForTests_DefaultCurrentUserAsRoot() {
	testsdetection.MustBeTesting()

	defaultOptions.rootUID = currentUserUID()
}
