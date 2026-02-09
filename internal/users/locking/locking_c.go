package userslocking

/*
#include <shadow.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/authd/internal/errno"
	"github.com/canonical/authd/log"
)

// writeLock is the default locking implementation.
func writeLock() error {
	errno.Lock()
	defer errno.Unlock()

	if C.lckpwdf() == 0 {
		log.Debug(context.Background(), "glibc lckpwdf: Local entries locked!")
		return nil
	}

	err := errno.Get()
	if errors.Is(err, errno.ErrIntr) {
		// lckpwdf sets errno to EINTR when a SIGALRM is received, which is expected when the lock times out.
		return ErrLockTimeout
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLock, err)
	}

	return ErrLock
}

// writeUnlock is the default unlocking implementation.
func writeUnlock() error {
	errno.Lock()
	defer errno.Unlock()

	if C.ulckpwdf() == 0 {
		log.Debug(context.Background(), "glibc lckpwdf: Local entries unlocked!")
		return nil
	}

	if err := errno.Get(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnlock, err)
	}

	return ErrUnlock
}
