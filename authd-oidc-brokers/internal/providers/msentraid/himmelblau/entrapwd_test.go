package himmelblau

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMFAInitError_Error(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAInitError
		want string
	}{
		"Without_AADSTS": {err: &MFAInitError{Message: "plain message"}, want: "plain message"},
		"With_AADSTS":    {err: &MFAInitError{AADSTS: 50126, Message: "bad credentials"}, want: "AADSTS50126: bad credentials"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestMFAInitError_IsMFAPollContinue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAInitError
		want bool
	}{
		"Poll_continue":             {err: &MFAInitError{Category: MFAErrorPollContinue}, want: true},
		"Denied":                    {err: &MFAInitError{Category: MFAErrorDenied}, want: false},
		"Required":                  {err: &MFAInitError{Category: MFAErrorRequired}, want: false},
		"Other":                     {err: &MFAInitError{Category: MFAErrorOther}, want: false},
		"Poll_continue_with_aadsts": {err: &MFAInitError{Category: MFAErrorPollContinue, AADSTS: 50126}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFAPollContinue())
		})
	}
}

func TestMFAInitError_IsMFADenied(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAInitError
		want bool
	}{
		"Denied_no_aadsts":   {err: &MFAInitError{Category: MFAErrorDenied}, want: true},
		"Denied_with_aadsts": {err: &MFAInitError{Category: MFAErrorDenied, AADSTS: 50126}, want: true},
		"Poll_continue":      {err: &MFAInitError{Category: MFAErrorPollContinue}, want: false},
		"Required":           {err: &MFAInitError{Category: MFAErrorRequired}, want: false},
		"Other":              {err: &MFAInitError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFADenied())
		})
	}
}

func TestMFAInitError_IsMFARequired(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAInitError
		want bool
	}{
		"Required":      {err: &MFAInitError{Category: MFAErrorRequired}, want: true},
		"Poll_continue": {err: &MFAInitError{Category: MFAErrorPollContinue}, want: false},
		"Denied":        {err: &MFAInitError{Category: MFAErrorDenied}, want: false},
		"Other":         {err: &MFAInitError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFARequired())
		})
	}
}

func TestMFAInitError_IsMFARetryableCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *MFAInitError
		want bool
	}{
		"Retryable_code":             {err: &MFAInitError{Category: MFAErrorRetryableCode}, want: true},
		"Retryable_code_with_aadsts": {err: &MFAInitError{Category: MFAErrorRetryableCode, AADSTS: 50126}, want: true},
		"Poll_continue":              {err: &MFAInitError{Category: MFAErrorPollContinue}, want: false},
		"Denied":                     {err: &MFAInitError{Category: MFAErrorDenied}, want: false},
		"Required":                   {err: &MFAInitError{Category: MFAErrorRequired}, want: false},
		"Other":                      {err: &MFAInitError{Category: MFAErrorOther}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.IsMFARetryableCode())
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
