// Package fido performs WebAuthn assertions against locally connected FIDO2
// security keys, producing the assertion format that Entra ID's MFA flow (via
// libhimmelblau) expects back as auth_data.
//
// The wire format is load-bearing: libhimmelblau posts buildAssertionJSON's
// output verbatim to Entra's ests-fido endpoint, so it is defined by that live
// server, not the W3C WebAuthn spec. ests-fido is Microsoft's own token-service
// endpoint, so its payload differs from the browser-to-relying-party
// PublicKeyCredential JSON: the assertion is a FLAT object (not the nested §6.3
// shape) and the challenge is base64url-ENCODED into clientDataJSON (the C
// accessor returns it unencoded). These are not workarounds for a bug here —
// himmelblau-idm's own PAM builds the byte-identical shape for the same
// endpoint (himmelblau_unix_common::auth::fido_auth), which is the reference
// this was matched against. "Correcting" either detail toward the W3C spec
// breaks login, so verify against a real Entra login before changing them.
package fido

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// relyingPartyOrigin is the WebAuthn origin of Entra ID's FIDO login page.
// Assertions signed for any other origin are rejected.
const relyingPartyOrigin = "https://login.microsoft.com"

// Sentinel errors returned by Authenticator implementations so the broker can
// route the failure (collect a PIN, let the user retry, or deny) without
// depending on libfido2 specifics.
var (
	// ErrNoDevice means no FIDO2 device is connected.
	ErrNoDevice = errors.New("no FIDO2 device found")
	// ErrNoCredentials means the connected device has no credential accepted
	// by the MFA challenge.
	ErrNoCredentials = errors.New("security key has no matching credential")
	// ErrPINRequired means the device requires a client PIN for this
	// assertion and none was provided.
	ErrPINRequired = errors.New("security key requires a PIN")
	// ErrPINInvalid means the provided PIN was wrong; the user can retry
	// with a new PIN (devices hard-block after repeated failures).
	ErrPINInvalid = errors.New("security key PIN is incorrect")
	// ErrPINBlocked means the device refuses PIN authentication until it is
	// reinserted or reset; the assertion cannot proceed.
	ErrPINBlocked = errors.New("security key PIN is blocked")
	// ErrTimeout means the user did not touch the device in time.
	ErrTimeout = errors.New("security key was not touched in time")
	// ErrCanceled means the assertion was canceled (e.g. the session ended).
	ErrCanceled = errors.New("FIDO assertion canceled")
)

// clientDataJSON builds the WebAuthn client data for a Get ceremony. The raw
// challenge is base64url-encoded (load-bearing; see the package doc).
func clientDataJSON(challenge string) []byte {
	data := struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}{
		Type:      "webauthn.get",
		Challenge: base64.RawURLEncoding.EncodeToString([]byte(challenge)),
		Origin:    relyingPartyOrigin,
	}
	// Marshaling a struct of strings cannot fail.
	out, _ := json.Marshal(data)
	return out
}

// buildAssertionJSON assembles the auth_data for a FidoKey method as a FLAT
// object (load-bearing; see the package doc). Fields are base64url-encoded
// without padding; an absent user handle serializes as an empty string.
func buildAssertionJSON(credentialID, clientData, authData, signature, userHandle []byte) (string, error) {
	b64url := base64.RawURLEncoding.EncodeToString
	response := struct {
		ID                string `json:"id"`
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	}{
		ID:                b64url(credentialID),
		AuthenticatorData: b64url(authData),
		ClientDataJSON:    b64url(clientData),
		Signature:         b64url(signature),
		UserHandle:        b64url(userHandle),
	}
	out, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal assertion response: %v", err)
	}
	return string(out), nil
}

// decodeAllowList decodes the credential IDs from the MFA flow's FIDO allow
// list. Entra ID sends bare standard-base64 credential IDs (which is what
// upstream himmelblau decodes), but some responses carry WebAuthn
// PublicKeyCredentialDescriptor JSON objects with a base64url id instead, so
// both forms are accepted. Undecodable entries are skipped; it is an error
// when a non-empty list yields no usable credential ID.
func decodeAllowList(entries []string) ([][]byte, error) {
	var credentialIDs [][]byte
	for _, entry := range entries {
		id, err := decodeAllowListEntry(entry)
		if err != nil {
			continue
		}
		credentialIDs = append(credentialIDs, id)
	}
	if len(entries) > 0 && len(credentialIDs) == 0 {
		return nil, fmt.Errorf("no credential ID in the FIDO allow list could be decoded")
	}
	return credentialIDs, nil
}

func decodeAllowListEntry(entry string) ([]byte, error) {
	var descriptor struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(entry), &descriptor); err == nil && descriptor.ID != "" {
		entry = descriptor.ID
	}

	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		// Reject a decoded empty credential ID (which every encoding produces
		// for an empty string without an error) rather than passing a
		// zero-length credential ID to the authenticator.
		if id, err := encoding.DecodeString(entry); err == nil && len(id) > 0 {
			return id, nil
		}
	}
	return nil, fmt.Errorf("credential ID is not valid base64: %q", entry)
}

// rawAuthData unwraps the CBOR byte string that libfido2 returns as the
// authenticator data (fido_assert_authdata_ptr is CBOR-wrapped). Entra ID
// expects the raw authenticator data bytes in the assertion response.
func rawAuthData(cborAuthData []byte) ([]byte, error) {
	if len(cborAuthData) == 0 {
		return nil, fmt.Errorf("empty authenticator data")
	}

	const majorTypeByteString = 2
	if cborAuthData[0]>>5 != majorTypeByteString {
		return nil, fmt.Errorf("authenticator data is not a CBOR byte string (leading byte %#x)", cborAuthData[0])
	}

	var length, offset int
	switch info := int(cborAuthData[0] & 0x1f); {
	case info < 24:
		length, offset = info, 1
	case info == 24: // one-byte length
		offset = 2
		if len(cborAuthData) < offset {
			return nil, fmt.Errorf("truncated CBOR length")
		}
		length = int(cborAuthData[1])
	case info == 25: // two-byte length
		offset = 3
		if len(cborAuthData) < offset {
			return nil, fmt.Errorf("truncated CBOR length")
		}
		length = int(binary.BigEndian.Uint16(cborAuthData[1:3]))
	case info == 26: // four-byte length
		offset = 5
		if len(cborAuthData) < offset {
			return nil, fmt.Errorf("truncated CBOR length")
		}
		length = int(binary.BigEndian.Uint32(cborAuthData[1:5]))
	default:
		// 27 (eight-byte) exceeds any plausible authenticator data size and
		// 28-31 (reserved/indefinite) are not produced by libfido2.
		return nil, fmt.Errorf("unsupported CBOR length encoding %#x", cborAuthData[0])
	}

	if len(cborAuthData) < offset+length {
		return nil, fmt.Errorf("truncated authenticator data: want %d bytes, have %d", length, len(cborAuthData)-offset)
	}
	return cborAuthData[offset : offset+length], nil
}
