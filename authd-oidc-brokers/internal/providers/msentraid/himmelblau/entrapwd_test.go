package himmelblau

import (
	"testing"

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
