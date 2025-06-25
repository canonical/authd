// Package localentries provides functions to access local passwd entries.
//
//nolint:dupl // This it not a duplicate of getgrent_c.go
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
	"unsafe"

	"github.com/ubuntu/decorate"
)

// Passwd represents a passwd entry.
type Passwd struct {
	Name  string
	UID   uint32
	Gecos string
}

var getpwentMu sync.Mutex

// GetPasswdEntries returns all passwd entries.
func GetPasswdEntries() (entries []Passwd, err error) {
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

		entries = append(entries, Passwd{
			Name:  C.GoString(passwdPtr.pw_name),
			UID:   uint32(passwdPtr.pw_uid),
			Gecos: C.GoString(passwdPtr.pw_gecos),
		})
	}
}

// ErrUserNotFound is returned when a user is not found.
var ErrUserNotFound = errors.New("user not found")

// GetPasswdByName returns the user with the given name.
func GetPasswdByName(name string) (p Passwd, err error) {
	decorate.OnError(&err, "getgrnam_r")

	var passwd C.struct_passwd
	var passwdPtr *C.struct_passwd
	buf := make([]C.char, 256)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pinner.Pin(&passwd)
	pinner.Pin(&buf[0])

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	for {
		ret := C.getpwnam_r(cName, &passwd, &buf[0], C.size_t(len(buf)), &passwdPtr)
		errno := syscall.Errno(ret)

		if errors.Is(errno, syscall.ERANGE) {
			buf = make([]C.char, len(buf)*2)
			pinner.Pin(&buf[0])
			continue
		}
		if (errors.Is(errno, syscall.Errno(0)) && passwdPtr == nil) ||
			errors.Is(errno, syscall.ENOENT) ||
			errors.Is(errno, syscall.ESRCH) ||
			errors.Is(errno, syscall.EBADF) ||
			errors.Is(errno, syscall.EPERM) {
			return Passwd{}, ErrUserNotFound
		}
		if !errors.Is(errno, syscall.Errno(0)) {
			return Passwd{}, errno
		}

		return Passwd{
			Name:  C.GoString(passwdPtr.pw_name),
			UID:   uint32(passwdPtr.pw_uid),
			Gecos: C.GoString(passwdPtr.pw_gecos),
		}, nil
	}
}
