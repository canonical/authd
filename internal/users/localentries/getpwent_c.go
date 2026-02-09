// Package localentries provides functions to access local passwd entries.
package localentries

/*
#include <stdlib.h>
#include <pwd.h>
#include <grp.h>
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
	"syscall"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/users/types"
)

var getpwentMu sync.Mutex

// getUserEntries returns all passwd entries.
func getUserEntries() (entries []types.UserEntry, err error) {
	decorate.OnError(&err, "getpwent_r")

	// This function repeatedly calls getpwent_r, which iterates over the records in the passwd database.
	// Use a mutex to avoid that parallel calls to this function interfere with each other.
	// It would be nice to use fgetpwent_r, that is thread safe, but it can only
	// iterate over a stream, while we want to iterate over all the NSS sources too.
	getpwentMu.Lock()
	defer getpwentMu.Unlock()

	C.setpwent()
	defer C.endpwent()

	var passwd C.struct_passwd
	var passwdPtr *C.struct_passwd
	buf := make([]C.char, 1024)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pinner.Pin(&passwd)
	pinner.Pin(&buf[0])

	for {
		ret := C.getpwent_r(&passwd, &buf[0], C.size_t(len(buf)), &passwdPtr)
		errno := syscall.Errno(ret)

		if errors.Is(errno, syscall.ERANGE) {
			buf = make([]C.char, len(buf)*2)
			pinner.Pin(&buf[0])
			continue
		}
		if errors.Is(errno, syscall.ENOENT) {
			return entries, nil
		}
		if !errors.Is(errno, syscall.Errno(0)) {
			return nil, errno
		}

		entries = append(entries, types.UserEntry{
			Name:  C.GoString(passwdPtr.pw_name),
			UID:   uint32(passwdPtr.pw_uid),
			GID:   uint32(passwdPtr.pw_gid),
			Gecos: C.GoString(passwdPtr.pw_gecos),
			Dir:   C.GoString(passwdPtr.pw_dir),
			Shell: C.GoString(passwdPtr.pw_shell),
		})
	}
}
