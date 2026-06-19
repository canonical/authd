package pam

/*
#include <security/pam_appl.h>
*/
import "C"

// Error is the Type for PAM Return types
type Error int

// Pam Return types
const (
	// OpenErr indicates a dlopen() failure when dynamically loading a
	// service module.
	ErrOpen Error = C.PAM_OPEN_ERR
	// ErrSymbol indicates a symbol not found.
	ErrSymbol Error = C.PAM_SYMBOL_ERR
	// ErrService indicates a error in service module.
	ErrService Error = C.PAM_SERVICE_ERR
	// ErrSystem indicates a system error.
	ErrSystem Error = C.PAM_SYSTEM_ERR
	// ErrBuf indicates a memory buffer error.
	ErrBuf Error = C.PAM_BUF_ERR
	// ErrPermDenied indicates a permission denied.
	ErrPermDenied Error = C.PAM_PERM_DENIED
	// ErrAuth indicates a authentication failure.
	ErrAuth Error = C.PAM_AUTH_ERR
	// ErrCredInsufficient indicates a can not access authentication data due to
	// insufficient credentials.
	ErrCredInsufficient Error = C.PAM_CRED_INSUFFICIENT
	// ErrAuthinfoUnavail indicates that the underlying authentication service
	// can not retrieve authentication information.
	ErrAuthinfoUnavail Error = C.PAM_AUTHINFO_UNAVAIL
	// ErrUserUnknown indicates a user not known to the underlying authentication
	// module.
	ErrUserUnknown Error = C.PAM_USER_UNKNOWN
	// ErrMaxtries indicates that an authentication service has maintained a retry
	// count which has been reached. No further retries should be attempted.
	ErrMaxtries Error = C.PAM_MAXTRIES
	// ErrNewAuthtokReqd indicates a new authentication token required. This is
	// normally returned if the machine security policies require that the
	// password should be changed because the password is nil or it has aged.
	ErrNewAuthtokReqd Error = C.PAM_NEW_AUTHTOK_REQD
	// ErrAcctExpired indicates that an user account has expired.
	ErrAcctExpired Error = C.PAM_ACCT_EXPIRED
	// ErrSession indicates a can not make/remove an entry for the
	// specified session.
	ErrSession Error = C.PAM_SESSION_ERR
	// ErrCredUnavail indicates that an underlying authentication service can not
	// retrieve user credentials.
	ErrCredUnavail Error = C.PAM_CRED_UNAVAIL
	// ErrCredExpired indicates that an user credentials expired.
	ErrCredExpired Error = C.PAM_CRED_EXPIRED
	// ErrCred indicates a failure setting user credentials.
	ErrCred Error = C.PAM_CRED_ERR
	// ErrNoModuleData indicates a no module specific data is present.
	ErrNoModuleData Error = C.PAM_NO_MODULE_DATA
	// ErrConv indicates a conversation error.
	ErrConv Error = C.PAM_CONV_ERR
	// ErrAuthtokErr indicates an authentication token manipulation error.
	ErrAuthtok Error = C.PAM_AUTHTOK_ERR
	// ErrAuthtokRecoveryErr indicates an authentication information cannot
	// be recovered.
	ErrAuthtokRecovery Error = C.PAM_AUTHTOK_RECOVERY_ERR
	// ErrAuthtokLockBusy indicates am authentication token lock busy.
	ErrAuthtokLockBusy Error = C.PAM_AUTHTOK_LOCK_BUSY
	// ErrAuthtokDisableAging indicates an authentication token aging disabled.
	ErrAuthtokDisableAging Error = C.PAM_AUTHTOK_DISABLE_AGING
	// ErrTryAgain indicates a preliminary check by password service.
	ErrTryAgain Error = C.PAM_TRY_AGAIN
	// ErrIgnore indicates to ignore underlying account module regardless of
	// whether the control flag is required, optional, or sufficient.
	ErrIgnore Error = C.PAM_IGNORE
	// ErrAbort indicates a critical error (module fail now request).
	ErrAbort Error = C.PAM_ABORT
	// ErrAuthtokExpired indicates an user's authentication token has expired.
	ErrAuthtokExpired Error = C.PAM_AUTHTOK_EXPIRED
	// ErrModuleUnknown indicates a module is not known.
	ErrModuleUnknown Error = C.PAM_MODULE_UNKNOWN
	// ErrBadItem indicates a bad item passed to pam_*_item().
	ErrBadItem Error = C.PAM_BAD_ITEM
	// ErrConvAgain indicates a conversation function is event driven and data
	// is not available yet.
	ErrConvAgain Error = C.PAM_CONV_AGAIN
	// ErrIncomplete indicates to please call this function again to complete
	// authentication stack. Before calling again, verify that conversation
	// is completed.
	ErrIncomplete Error = C.PAM_INCOMPLETE
)

// Error returns the error message for the given status.
func (status Error) Error() string {
	return C.GoString(C.pam_strerror(nil, C.int(status)))
}
