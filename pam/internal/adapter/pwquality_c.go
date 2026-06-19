package adapter

// #cgo pkg-config: pwquality
// #include <stdlib.h>
// #include <pwquality.h>
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

var passwordQualityMu sync.Mutex

// checkPasswordQuality checks the quality of the new password using the pwquality library.
func checkPasswordQuality(oldPassword, newPassword string) error {
	passwordQualityMu.Lock()
	defer passwordQualityMu.Unlock()

	pwq := C.pwquality_default_settings()
	if pwq == nil {
		return errors.New("could not allocate pw quality default settings")
	}
	defer C.pwquality_free_settings(pwq)

	var auxErr *C.char
	auxErrPointer := unsafe.Pointer(auxErr)

	// Load pwquality configuration (from /etc/security/pwquality.conf)
	if ret := C.pwquality_read_config(pwq, nil, &auxErrPointer); ret < 0 {
		var buf [C.PWQ_MAX_ERROR_MESSAGE_LEN]C.char
		errMsg := C.GoString(C.pwquality_strerror(&buf[0], C.size_t(len(buf)), ret, auxErrPointer))
		return fmt.Errorf("can't ready pwquality configuration: %s", errMsg)
	}

	oldC := C.CString(oldPassword)
	defer C.free(unsafe.Pointer(oldC))

	newC := C.CString(newPassword)
	defer C.free(unsafe.Pointer(newC))

	if ret := C.pwquality_check(pwq, newC, oldC, nil, &auxErrPointer); ret < 0 {
		var buf [C.PWQ_MAX_ERROR_MESSAGE_LEN]C.char
		errMsg := C.GoString(C.pwquality_strerror(&buf[0], C.size_t(len(buf)), ret, auxErrPointer))
		return errors.New(errMsg)
	}
	return nil
}
