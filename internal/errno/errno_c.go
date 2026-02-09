// Package errno provide utilities to use C errno from the Go side.
package errno

/*
#include <errno.h>
#include <string.h>

static void unset_errno(void) {
  errno = 0;
}

static int get_errno(void) {
  return errno;
}

*/
import "C"

import (
	"sync"
)

// Error is the type for the errno error.
type Error C.int

func (errno Error) Error() string {
	return C.GoString(C.strerror(C.int(errno)))
}

const (
	// ErrNoEnt is the errno ENOENT.
	ErrNoEnt Error = C.ENOENT
	// ErrSrch is the errno ESRCH.
	ErrSrch Error = C.ESRCH
	// ErrBadf is the errno EBADF.
	ErrBadf Error = C.EBADF
	// ErrPerm is the errno EPERM.
	ErrPerm Error = C.EPERM
	// ErrIntr is the errno EINTR.
	ErrIntr Error = C.EINTR
)

// All these functions are expected to be called while this mutex is locked.
var mu sync.Mutex

var getErrno = func() int { return int(C.get_errno()) }
var unsetErrno = func() { C.unset_errno() }

// Lock the usage of errno.
func Lock() {
	mu.Lock()
	unsetErrno()
}

// Unlock unlocks the errno package for being re-used.
func Unlock() {
	unsetErrno()
	mu.Unlock()
}

// Get gets the current errno as [Error].
func Get() error {
	if mu.TryLock() {
		mu.Unlock()
		panic("Using errno without locking!")
	}

	if errno := getErrno(); errno != 0 {
		return Error(errno)
	}
	return nil
}
