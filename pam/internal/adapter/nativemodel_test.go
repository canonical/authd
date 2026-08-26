package adapter

import (
	"errors"
	"testing"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/brokers/layouts/entries"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/canonical/authd/pam/internal/proto"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

// recordedConv is one recorded call to [recordingConvHandler.RespondPAM].
type recordedConv struct {
	style  pam.Style
	prompt string
}

// recordingConvHandler is a [pam.ConversationHandler] double that records
// every text conversation call it receives and replies with a canned value.
type recordingConvHandler struct {
	calls []recordedConv
	reply string
	err   error
}

func (h *recordingConvHandler) RespondPAM(style pam.Style, prompt string) (string, error) {
	h.calls = append(h.calls, recordedConv{style, prompt})
	return h.reply, h.err
}

func (h *recordingConvHandler) RespondPAMBinary(pam.BinaryPointer) (pam.BinaryPointer, error) {
	return nil, errors.New("binary conversation not supported in this test")
}

func TestNativeModelFormatInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		serviceName string
		title       string
		message     string
		want        string
	}{
		"Regular_service_with_message": {
			title:   "Authentication",
			message: "Enter a password",
			want:    "== Authentication ==\nEnter a password",
		},
		"Regular_service_without_message": {
			title: "Authentication",
			want:  "== Authentication ==",
		},
		"Polkit_service_with_message": {
			serviceName: polkitServiceName,
			title:       "Authentication",
			message:     "Enter a password",
			want:        "Enter a password",
		},
		"Polkit_service_without_message": {
			serviceName: polkitServiceName,
			title:       "Authentication",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := nativeModel{serviceName: tc.serviceName}
			require.Equal(t, tc.want, m.formatInfo(tc.title, tc.message))
		})
	}
}

func TestNativeModelHandleFormChallenge(t *testing.T) {
	t.Parallel()

	const waitLabel = "Open your Authenticator app, and enter the number '60' to sign in."

	tests := map[string]struct {
		label                string
		entry                string
		hasWait              bool
		serviceName          string
		userSelectionAllowed bool
		currentStage         proto.Stage
		convReply            string
		convErr              error

		wantCallStyles []pam.Style
		wantInfoMsg    string
		wantPrompt     string
		wantWait       bool
		wantSecret     string
		wantPamError   bool
	}{
		"Wait_only_form_sends_info_and_waits_without_prompting": {
			label:          waitLabel,
			hasWait:        true,
			serviceName:    polkitServiceName,
			wantCallStyles: []pam.Style{pam.TextInfo},
			wantInfoMsg:    waitLabel,
			wantWait:       true,
		},
		"Wait_only_form_with_go_back_available_still_waits": {
			label:                waitLabel,
			hasWait:              true,
			serviceName:          polkitServiceName,
			userSelectionAllowed: true,
			currentStage:         proto.Stage_challenge,
			wantCallStyles:       []pam.Style{pam.TextInfo},
			wantInfoMsg:          waitLabel,
			wantWait:             true,
		},
		"Wait_only_form_on_native_falls_back_to_prompting_for_empty_response": {
			label:          waitLabel,
			hasWait:        true,
			wantCallStyles: []pam.Style{pam.TextInfo, pam.PromptEchoOn},
			wantInfoMsg:    "Press Enter to wait for authentication",
			wantWait:       true,
		},
		"Form_with_entry_and_wait_still_prompts_for_secret": {
			label:          "Enter your password",
			entry:          entries.CharsPassword,
			hasWait:        true,
			convReply:      "hunter2",
			wantCallStyles: []pam.Style{pam.TextInfo, pam.PromptEchoOff},
			wantSecret:     "hunter2",
		},
		"Polkit_form_shows_label_as_info_and_keeps_input_hint_empty": {
			label:          "Enter your Entra ID password",
			entry:          entries.CharsPassword,
			serviceName:    polkitServiceName,
			convReply:      "hunter2",
			wantCallStyles: []pam.Style{pam.TextInfo, pam.PromptEchoOff},
			wantInfoMsg:    "Enter your Entra ID password",
			wantPrompt:     " \n> ",
			wantSecret:     "hunter2",
		},
		"SendInfo_error_is_propagated_instead_of_waiting": {
			label:          waitLabel,
			hasWait:        true,
			convErr:        errors.New("conversation boom"),
			wantCallStyles: []pam.Style{pam.TextInfo},
			wantPamError:   true,
		},
		"Wait_only_form_on_polkit_sendInfo_error_returns_pam_error": {
			label:          waitLabel,
			hasWait:        true,
			serviceName:    polkitServiceName,
			convErr:        errors.New("conversation boom"),
			wantCallStyles: []pam.Style{pam.TextInfo},
			wantPamError:   true,
		},
		"Missing_label_returns_pam_error_without_any_conversation": {
			label:          "",
			hasWait:        true,
			wantCallStyles: nil,
			wantPamError:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := &recordingConvHandler{reply: tc.convReply, err: tc.convErr}
			m := nativeModel{
				pamMTx:               pam_test.NewModuleTransactionDummy(handler),
				serviceName:          tc.serviceName,
				interactive:          true,
				userSelectionAllowed: tc.userSelectionAllowed,
				currentStage:         tc.currentStage,
				uiLayout: &authd.UILayout{
					Type:  layouts.Form,
					Label: &tc.label,
					Entry: &tc.entry,
				},
			}

			cmd := m.handleFormChallenge(tc.hasWait)
			require.NotNil(t, cmd)
			msg := cmd()

			var gotStyles []pam.Style
			for _, c := range handler.calls {
				gotStyles = append(gotStyles, c.style)
			}
			require.Equal(t, tc.wantCallStyles, gotStyles)

			if tc.wantInfoMsg != "" {
				require.Contains(t, handler.calls[0].prompt, tc.wantInfoMsg)
			}
			if tc.wantPrompt != "" {
				require.Equal(t, tc.wantPrompt, handler.calls[len(handler.calls)-1].prompt)
			}

			switch {
			case tc.wantPamError:
				_, ok := msg.(pamError)
				require.True(t, ok, "want a pamError, got %#v", msg)

			case tc.wantWait:
				gotEvent, ok := msg.(isAuthenticatedRequested)
				require.True(t, ok, "want isAuthenticatedRequested, got %#v", msg)
				waitItem, ok := gotEvent.item.(*authd.IARequest_AuthenticationData_Wait)
				require.True(t, ok, "want a Wait authentication item, got %#v", gotEvent.item)
				require.Equal(t, layouts.True, waitItem.Wait)

			case tc.wantSecret != "":
				gotEvent, ok := msg.(isAuthenticatedRequested)
				require.True(t, ok, "want isAuthenticatedRequested, got %#v", msg)
				secretItem, ok := gotEvent.item.(*authd.IARequest_AuthenticationData_Secret)
				require.True(t, ok, "want a Secret authentication item, got %#v", gotEvent.item)
				require.Equal(t, tc.wantSecret, secretItem.Secret)
			}
		})
	}
}
