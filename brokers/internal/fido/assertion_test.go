package fido

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientDataJSON(t *testing.T) {
	t.Parallel()

	got := clientDataJSON("test-challenge")

	var parsed struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed), "clientDataJSON must be valid JSON")
	require.Equal(t, "webauthn.get", parsed.Type)
	require.Equal(t, "dGVzdC1jaGFsbGVuZ2U", parsed.Challenge,
		"challenge must be base64url(raw challenge) without padding")
	require.Equal(t, "https://login.microsoft.com", parsed.Origin)
}

func TestBuildAssertionJSON(t *testing.T) {
	t.Parallel()

	credentialID := []byte{0x01, 0x02, 0xfb, 0xff}
	clientData := clientDataJSON("test-challenge")
	authData := []byte{0xaa, 0xbb, 0xcc}
	signature := []byte{0x30, 0x45, 0x02, 0x20}
	userHandle := []byte("user-id")

	got, err := buildAssertionJSON(credentialID, clientData, authData, signature, userHandle)
	require.NoError(t, err)

	var parsed struct {
		ID                string `json:"id"`
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &parsed), "assertion must be valid JSON")

	b64url := base64.RawURLEncoding.EncodeToString
	require.Equal(t, b64url(credentialID), parsed.ID)
	require.Equal(t, b64url(clientData), parsed.ClientDataJSON)
	require.Equal(t, b64url(authData), parsed.AuthenticatorData)
	require.Equal(t, b64url(signature), parsed.Signature)
	require.Equal(t, b64url(userHandle), parsed.UserHandle)
}

func TestBuildAssertionJSONEmptyUserHandle(t *testing.T) {
	t.Parallel()

	got, err := buildAssertionJSON([]byte{0x01}, []byte("{}"), []byte{0x02}, []byte{0x03}, nil)
	require.NoError(t, err)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal([]byte(got), &parsed))
	require.Equal(t, "", parsed["userHandle"],
		"an absent user handle must serialize as an empty string, like upstream himmelblau")
}

func TestDecodeAllowList(t *testing.T) {
	t.Parallel()

	credID := []byte{0xfb, 0xef, 0xff, 0x01, 0x02}
	stdB64 := base64.StdEncoding.EncodeToString(credID)       // contains + / =
	rawURLB64 := base64.RawURLEncoding.EncodeToString(credID) // contains - _

	tests := map[string]struct {
		entries []string
		want    [][]byte
		wantErr bool
	}{
		"standard base64 entries, like upstream himmelblau": {
			entries: []string{stdB64},
			want:    [][]byte{credID},
		},
		"base64url entries": {
			entries: []string{rawURLB64},
			want:    [][]byte{credID},
		},
		"webauthn JSON descriptor entries": {
			entries: []string{`{"type":"public-key","id":"` + rawURLB64 + `"}`},
			want:    [][]byte{credID},
		},
		"undecodable entries are skipped": {
			entries: []string{"!!!not-base64!!!", stdB64},
			want:    [][]byte{credID},
		},
		"empty entries are skipped, not decoded as a zero-length credential ID": {
			entries: []string{"", stdB64},
			want:    [][]byte{credID},
		},
		"empty list": {
			entries: nil,
			want:    nil,
		},
		"error when nothing is decodable": {
			entries: []string{"!!!not-base64!!!"},
			wantErr: true,
		},
		"error when the only entry is empty": {
			entries: []string{""},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeAllowList(tc.entries)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRawAuthData(t *testing.T) {
	t.Parallel()

	payload := make([]byte, 37) // typical authenticator data length
	for i := range payload {
		payload[i] = byte(i)
	}

	tests := map[string]struct {
		input   []byte
		want    []byte
		wantErr bool
	}{
		"short form length": {
			input: append([]byte{0x40 | 5}, payload[:5]...),
			want:  payload[:5],
		},
		"one-byte length (0x58)": {
			input: append([]byte{0x58, 37}, payload...),
			want:  payload,
		},
		"two-byte length (0x59)": {
			input: append([]byte{0x59, 0x00, 37}, payload...),
			want:  payload,
		},
		"four-byte length (0x5a)": {
			input: append([]byte{0x5a, 0x00, 0x00, 0x00, 37}, payload...),
			want:  payload,
		},
		"empty input": {
			input:   nil,
			wantErr: true,
		},
		"not a byte string (CBOR array)": {
			input:   []byte{0x81, 0x01},
			wantErr: true,
		},
		"indefinite length is unsupported": {
			input:   []byte{0x5f, 0x41, 0x01, 0xff},
			wantErr: true,
		},
		"truncated payload": {
			input:   []byte{0x58, 37, 0x01, 0x02},
			wantErr: true,
		},
		"truncated length": {
			input:   []byte{0x59, 0x00},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := rawAuthData(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
