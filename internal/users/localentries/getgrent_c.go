// Package localentries provides functions to access the local user and group database.
//
//nolint:dupl // This it not a duplicate of getpwent_c.go
package localentries

/*
#define _GNU_SOURCE

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

// Group represents a group entry.
type Group struct {
	Name   string
	GID    uint32
	Passwd string
}

var getgrentMu sync.Mutex

// GetGroupEntries returns all group entries.
func GetGroupEntries() (entries []Group, err error) {
	decorate.OnError(&err, "getgrent_r")

	// This function repeatedly calls getgrent_r, which iterates over the records in the group database.
	// Use a mutex to avoid that parallel calls to this function interfere with each other.
	// It would be nice to use fgetgrent_r, that is thread safe, but it can only
	// iterate over a stream, while we want to iterate over all the NSS sources too.
	getgrentMu.Lock()
	defer getgrentMu.Unlock()

	C.setgrent()
	defer C.endgrent()

	var group C.struct_group
	var groupPtr *C.struct_group
	buf := make([]C.char, 1024)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pinner.Pin(&group)
	pinner.Pin(&buf[0])

	for {
		ret := C.getgrent_r(&group, &buf[0], C.size_t(len(buf)), &groupPtr)
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

		entries = append(entries, Group{
			Name:   C.GoString(groupPtr.gr_name),
			Passwd: C.GoString(groupPtr.gr_passwd),
			GID:    uint32(groupPtr.gr_gid),
		})
	}
}

// ErrGroupNotFound is returned when a group is not found.
var ErrGroupNotFound = errors.New("group not found")

// GetGroupByName returns the group with the given name.
func GetGroupByName(name string) (g Group, err error) {
	decorate.OnError(&err, "getgrnam_r")

	var group C.struct_group
	var groupPtr *C.struct_group
	buf := make([]C.char, 256)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pinner.Pin(&group)
	pinner.Pin(&buf[0])

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	for {
		ret := C.getgrnam_r(cName, &group, &buf[0], C.size_t(len(buf)), &groupPtr)
		errno := syscall.Errno(ret)

		if errors.Is(errno, syscall.ERANGE) {
			buf = make([]C.char, len(buf)*2)
			pinner.Pin(&buf[0])
			continue
		}
		if (errors.Is(errno, syscall.Errno(0)) && groupPtr == nil) ||
			errors.Is(errno, syscall.ENOENT) ||
			errors.Is(errno, syscall.ESRCH) ||
			errors.Is(errno, syscall.EBADF) ||
			errors.Is(errno, syscall.EPERM) {
			return Group{}, ErrGroupNotFound
		}
		if !errors.Is(errno, syscall.Errno(0)) {
			return Group{}, errno
		}

		return Group{
			Name:   C.GoString(groupPtr.gr_name),
			GID:    uint32(groupPtr.gr_gid),
			Passwd: C.GoString(groupPtr.gr_passwd),
		}, nil
	}
}
