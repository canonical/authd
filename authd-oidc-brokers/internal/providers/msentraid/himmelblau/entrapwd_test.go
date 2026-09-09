package himmelblau

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMFAError_Error(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want string
	}{
		"Without_AADSTS": {err: &MFAError{Message: "plain message"}, want: "plain message"},
		"With_AADSTS":    {err: &MFAError{AADSTS: 50126, Message: "bad credentials"}, want: "AADSTS50126: bad credentials"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestMFAError_IsMFATransient(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"External_server_retryable": {err: &MFAError{AADSTS: externalServerRetryableErrorCode}, want: true},
		"Tenant_throttling":         {err: &MFAError{AADSTS: tenantThrottlingErrorCode}, want: true},
		"Other_AADSTS":              {err: &MFAError{AADSTS: 50126}, want: false},
		"Without_AADSTS":            {err: &MFAError{}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFATransient())
		})
	}
}

func TestMFAError_IsMFAPollContinue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"Poll_continue":             {err: &MFAError{Category: MFAErrorPollContinue}, want: true},
		"Denied":                    {err: &MFAError{Category: MFAErrorDenied}, want: false},
		"Required":                  {err: &MFAError{Category: MFAErrorRequired}, want: false},
		"Other":                     {err: &MFAError{Category: MFAErrorOther}, want: false},
		"Poll_continue_with_aadsts": {err: &MFAError{Category: MFAErrorPollContinue, AADSTS: 50126}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFAPollContinue())
		})
	}
}

func TestMFAError_IsMFADenied(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"Denied_no_aadsts":   {err: &MFAError{Category: MFAErrorDenied}, want: true},
		"Denied_with_aadsts": {err: &MFAError{Category: MFAErrorDenied, AADSTS: 50126}, want: true},
		"Poll_continue":      {err: &MFAError{Category: MFAErrorPollContinue}, want: false},
		"Required":           {err: &MFAError{Category: MFAErrorRequired}, want: false},
		"Other":              {err: &MFAError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFADenied())
		})
	}
}

func TestMFAError_IsMFARequired(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"Required":      {err: &MFAError{Category: MFAErrorRequired}, want: true},
		"Poll_continue": {err: &MFAError{Category: MFAErrorPollContinue}, want: false},
		"Denied":        {err: &MFAError{Category: MFAErrorDenied}, want: false},
		"Other":         {err: &MFAError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFARequired())
		})
	}
}

func TestMFAError_IsMFARetryableCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"Retryable_code":             {err: &MFAError{Category: MFAErrorRetryableCode}, want: true},
		"Retryable_code_with_aadsts": {err: &MFAError{Category: MFAErrorRetryableCode, AADSTS: 50126}, want: true},
		"Poll_continue":              {err: &MFAError{Category: MFAErrorPollContinue}, want: false},
		"Denied":                     {err: &MFAError{Category: MFAErrorDenied}, want: false},
		"Required":                   {err: &MFAError{Category: MFAErrorRequired}, want: false},
		"Other":                      {err: &MFAError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFARetryableCode())
		})
	}
}

func TestMFAError_IsMFAUserNotFound(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAError
		want bool
	}{
		"User_not_found_AADSTS": {err: &MFAError{AADSTS: userNotFoundErrorCode}, want: true},
		"Other":                 {err: &MFAError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFAUserNotFound())
		})
	}
}

func TestFreeMFAFlowState_NilSafe(t *testing.T) {
	t.Parallel()

	// Must not panic on nil.
	FreeMFAFlowState(nil)

	// Must not panic on a state with no release func, and must reset opaque.
	flow := &MFAFlowState{opaque: "data"}
	FreeMFAFlowState(flow)
	require.Nil(t, flow.opaque)

	// Must call release once and clear it.
	released := 0
	flow = &MFAFlowState{opaque: "data", release: func() { released++ }}
	FreeMFAFlowState(flow)
	require.Equal(t, 1, released)
	require.Nil(t, flow.opaque)

	// A subsequent call must be a no-op.
	FreeMFAFlowState(flow)
	require.Equal(t, 1, released)
}

func TestRetryTransientInitiate(t *testing.T) {
	t.Parallel()

	// Zero the backoff so the test does not sleep. The cancelled-context
	// case uses a long delay so only the context branch of the retry
	// select is ready, keeping it deterministic.
	zeroDelays := []time.Duration{0, 0}
	transientErr := &MFAError{AADSTS: externalServerRetryableErrorCode}
	otherMFNErr := &MFAError{AADSTS: 50126}
	plainErr := errors.New("connection reset")
	okFlow := &MFAFlowState{}

	tests := map[string]struct {
		errs      []error
		cancelCtx bool
		wantFlow  *MFAFlowState
		wantErr   error
		wantCalls int
	}{
		"Success_on_first_attempt": {
			errs:      []error{nil},
			wantFlow:  okFlow,
			wantCalls: 1,
		},
		"Transient_then_success": {
			errs:      []error{transientErr, nil},
			wantFlow:  okFlow,
			wantCalls: 2,
		},
		"Retry_budget_exhausted": {
			errs:      []error{transientErr, transientErr, transientErr},
			wantErr:   transientErr,
			wantCalls: 3,
		},
		"Non_transient_MFA_error_not_retried": {
			errs:      []error{otherMFNErr},
			wantErr:   otherMFNErr,
			wantCalls: 1,
		},
		"Non_MFA_error_not_retried": {
			errs:      []error{plainErr},
			wantErr:   plainErr,
			wantCalls: 1,
		},
		"Cancelled_context_stops_retries": {
			errs:      []error{transientErr, nil},
			cancelCtx: true,
			wantErr:   transientErr,
			wantCalls: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			var calls int
			delays := zeroDelays
			if tc.cancelCtx {
				delays = []time.Duration{time.Hour}
			}
			flow, err := retryTransientInitiate(ctx, delays, func() (*MFAFlowState, error) {
				callErr := tc.errs[len(tc.errs)-1]
				if calls < len(tc.errs) {
					callErr = tc.errs[calls]
				}
				calls++
				if callErr != nil {
					return nil, callErr
				}
				return okFlow, nil
			})

			require.Equal(t, tc.wantCalls, calls)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tc.wantFlow != nil {
				require.Same(t, tc.wantFlow, flow)
			} else {
				require.Nil(t, flow)
			}
		})
	}
}
