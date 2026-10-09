//go:build withmsentraid

package msentraid_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid"
	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/msentraid/himmelblau"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/canonical/authd/log"
	"github.com/golang-jwt/jwt/v5"
	msgraphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

var discoveryURLMu sync.RWMutex

func TestNew(t *testing.T) {
	p := msentraid.New()

	require.NotEmpty(t, p, "New should return a non-empty provider")
}

func TestParseUnixID(t *testing.T) {
	t.Parallel()

	validInt64 := int64(1001)
	validFloat64 := float64(1002)
	validNumber := json.Number("1003")
	valid := uint32(1001)

	tests := map[string]struct {
		value   any
		want    *uint32
		wantErr bool
	}{
		"Absent":                {value: nil},
		"Int64":                 {value: int64(1001), want: &valid},
		"Pointer_int64":         {value: &validInt64, want: &valid},
		"Float64":               {value: float64(1002), want: func() *uint32 { v := uint32(1002); return &v }()},
		"Pointer_float64":       {value: &validFloat64, want: func() *uint32 { v := uint32(1002); return &v }()},
		"JSON_number":           {value: validNumber, want: func() *uint32 { v := uint32(1003); return &v }()},
		"String_is_rejected":    {value: "1001", wantErr: true},
		"Boolean_is_rejected":   {value: true, wantErr: true},
		"Negative_is_rejected":  {value: int64(-1), wantErr: true},
		"Zero_is_rejected":      {value: int64(0), wantErr: true},
		"Fraction_is_rejected":  {value: float64(1001.5), wantErr: true},
		"Too_large_is_rejected": {value: int64(2147483648), wantErr: true},
		"Reserved_65534":        {value: int64(65534), wantErr: true},
		"Reserved_65535":        {value: int64(65535), wantErr: true},
		"Reserved_max_uint32":   {value: uint64(1<<32 - 1), wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := msentraid.ParseUnixID(tc.value)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestProcessSecurityGroupsWithGID(t *testing.T) {
	t.Parallel()

	makeGroup := func(id, name string, gid any) msgraphmodels.Groupable {
		group := msgraphmodels.NewGroup()
		group.SetId(&id)
		group.SetDisplayName(&name)
		securityEnabled := true
		group.SetSecurityEnabled(&securityEnabled)
		group.SetAdditionalData(map[string]any{"extension_gidNumber": gid})
		return group
	}

	t.Run("Valid_remote_and_local_groups", func(t *testing.T) {
		t.Parallel()

		groups, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("remote-id", "Engineering", int64(2001)),
			makeGroup("local-id", "linux-sudo", int64(9999)),
		}, "extension_gidNumber", true)
		require.NoError(t, err)
		require.Len(t, groups, 2)
		require.Equal(t, "engineering", groups[0].Name)
		require.Equal(t, "remote-id", groups[0].UGID)
		require.NotNil(t, groups[0].GID)
		require.Equal(t, uint32(2001), *groups[0].GID)
		require.Equal(t, info.Group{Name: "sudo"}, groups[1])
	})

	t.Run("Conflicting_duplicate_GIDs_are_omitted_when_optional", func(t *testing.T) {
		t.Parallel()

		groups, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("first", "Engineering", int64(2001)),
			makeGroup("second", "engineering", int64(2002)),
		}, "extension_gidNumber", false)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Nil(t, groups[0].GID)
	})

	t.Run("Conflicting_duplicate_GIDs_fail_when_required", func(t *testing.T) {
		t.Parallel()

		_, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("first", "Engineering", int64(2001)),
			makeGroup("second", "engineering", int64(2002)),
		}, "extension_gidNumber", true)
		require.ErrorIs(t, err, info.ErrUnixGIDRequired)
		require.Contains(t, err.Error(), "conflicting Unix GIDs")
	})

	t.Run("Malformed_optional_GID_is_omitted", func(t *testing.T) {
		t.Parallel()

		groups, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("remote-id", "Engineering", "2001"),
		}, "extension_gidNumber", false)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Nil(t, groups[0].GID)
	})

	t.Run("Malformed_required_GID_fails", func(t *testing.T) {
		t.Parallel()

		_, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("remote-id", "Engineering", "2001"),
		}, "extension_gidNumber", true)
		require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
		require.ErrorIs(t, err, info.ErrUnixGIDRequired)
	})

	t.Run("Missing_required_GID_fails", func(t *testing.T) {
		t.Parallel()

		_, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("remote-id", "Engineering", nil),
		}, "extension_gidNumber", true)
		require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
		require.ErrorIs(t, err, info.ErrUnixGIDRequired)
	})

	t.Run("Duplicate_missing_and_valid_GIDs_are_omitted", func(t *testing.T) {
		t.Parallel()

		groups, err := msentraid.ProcessSecurityGroupsWithGID([]msgraphmodels.Groupable{
			makeGroup("first", "Engineering", "not-a-number"),
			makeGroup("second", "engineering", int64(2002)),
		}, "extension_gidNumber", false)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Nil(t, groups[0].GID)
	})
}

func TestGroupSelectFields(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"id", "displayName", "securityEnabled", "groupTypes"}, msentraid.GroupSelectFields(""))
	require.Equal(t, []string{"id", "displayName", "securityEnabled", "groupTypes", "extension_abc_gidNumber"}, msentraid.GroupSelectFields("extension_abc_gidNumber"))
}

func TestGraphScopesForUnixAttributes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config info.UnixAttributeConfig
		want   []string
	}{
		"Groups_only": {
			want: []string{"GroupMember.Read.All"},
		},
		"GID_only": {
			config: info.UnixAttributeConfig{GIDAttribute: "extension_gidNumber"},
			want:   []string{"GroupMember.Read.All"},
		},
		"UID": {
			config: info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber"},
			want:   []string{"GroupMember.Read.All", "User.Read"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := msentraid.GraphScopesForUnixAttributes(tc.config)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMissingGraphScope(t *testing.T) {
	t.Parallel()

	requiredScopes := []string{"GroupMember.Read.All", "User.Read"}
	require.Empty(t, msentraid.MissingGraphScope(requiredScopes, requiredScopes))
	require.Equal(
		t,
		"User.Read",
		msentraid.MissingGraphScope([]string{"GroupMember.Read.All"}, requiredScopes),
	)
}

func TestEnrichUserWithUnixAttributesRequiresUserReadForUID(t *testing.T) {
	t.Parallel()

	var graphRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		graphRequests.Add(1)
		http.Error(w, "unexpected Graph request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All",
	})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)

	_, err = msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": server.URL},
		nil,
		info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber"},
	)
	require.ErrorContains(t, err, "missing the User.Read permission")
	require.Zero(t, graphRequests.Load())
}

func TestEnrichUserWithUnixAttributesDoesNotLogDeviceRegistrationData(t *testing.T) {
	registrationData := []byte(`{"cert_key":"registration-secret"} trailing`)
	var loggedMessage string
	log.SetLevelHandler(log.NoticeLevel, func(_ context.Context, _ log.Level, format string, args ...interface{}) {
		loggedMessage = fmt.Sprintf(format, args...)
	})
	t.Cleanup(func() {
		log.SetLevelHandler(log.NoticeLevel, nil)
	})

	_, err := msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: "invalid-token"},
		nil,
		registrationData,
		info.UnixAttributeConfig{},
	)
	require.Error(t, err)
	require.Contains(t, loggedMessage, "Could not decode device registration data")
	require.NotContains(t, loggedMessage, "registration-secret")
}

func TestEnrichUserWithUnixAttributesNeedsOnlyGroupScopeForGID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/me/transitiveMemberOf/graph.group", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"value":[]}`)
	}))
	t.Cleanup(server.Close)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All",
	})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)

	got, err := msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": server.URL},
		nil,
		info.UnixAttributeConfig{GIDAttribute: "extension_gidNumber"},
	)
	require.NoError(t, err)
	require.Empty(t, got.Groups)
}

func TestEnrichUserWithUnixAttributes(t *testing.T) {
	t.Parallel()

	var userRequests, groupRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/me":
			userRequests.Add(1)
			require.Equal(t, "extension_uidNumber", r.URL.Query().Get("$select"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"extension_uidNumber":1001}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/me/transitiveMemberOf/graph.group"):
			groupRequests.Add(1)
			require.ElementsMatch(t, []string{"id", "displayName", "securityEnabled", "groupTypes", "extension_gidNumber"}, strings.Split(r.URL.Query().Get("$select"), ","))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"value":[{"id":"group-id","displayName":"Engineering","securityEnabled":true,"groupTypes":[],"extension_gidNumber":2001}]}`)
		default:
			t.Fatalf("unexpected Graph request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All User.Read",
		"oid": "user-id",
	})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)

	got, err := msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": server.URL},
		nil,
		info.UnixAttributeConfig{
			UIDAttribute: "extension_uidNumber",
			GIDAttribute: "extension_gidNumber",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, got.UID)
	require.Equal(t, uint32(1001), *got.UID)
	require.Len(t, got.Groups, 1)
	require.NotNil(t, got.Groups[0].GID)
	require.Equal(t, uint32(2001), *got.Groups[0].GID)
	require.Equal(t, int32(1), userRequests.Load())
	require.Equal(t, int32(1), groupRequests.Load())
}

func TestEnrichUserWithUnixAttributesAppOnlyLeavesOptionalUIDUnavailable(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"oid": "user-id"})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)

	provider := msentraid.New()
	provider.SetGraphClientSecret("client-secret")
	got, err := provider.EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber"},
	)
	require.NoError(t, err)
	require.Nil(t, got.UID)
	require.Len(t, got.Groups, 2)
}

func TestEnrichUserWithUnixAttributesAppOnlyRejectsRequiredUID(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"oid": "user-id"})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)

	provider := msentraid.New()
	provider.SetGraphClientSecret("client-secret")
	_, err = provider.EnrichUserWithUnixAttributes(
		context.Background(),
		info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber", UIDRequired: true},
	)
	require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
	require.ErrorIs(t, err, info.ErrUnixUIDRequired)
}

func TestEnrichUserWithUnixAttributesClearsNullUID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me":
			_, _ = fmt.Fprint(w, `{"extension_uidNumber":null}`)
		case "/me/transitiveMemberOf/graph.group":
			_, _ = fmt.Fprint(w, `{"value":[]}`)
		default:
			t.Fatalf("unexpected Graph request: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"scp": "GroupMember.Read.All User.Read"})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)
	previousUID := uint32(1001)
	got, err := msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.User{Name: "alice@example.com", UID: &previousUID},
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": server.URL},
		nil,
		info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber"},
	)
	require.NoError(t, err)
	require.Nil(t, got.UID)
}

func TestEnrichUserWithUnixAttributesRejectsUnavailableRequiredUID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Missing":   `{}`,
		"Null":      `{"extension_uidNumber":null}`,
		"Malformed": `{"extension_uidNumber":"1001"}`,
	}
	for name, userResponse := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/me", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, userResponse)
			}))
			t.Cleanup(server.Close)

			accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"scp": "GroupMember.Read.All User.Read"})
			accessTokenString, err := accessToken.SignedString(testutils.MockKey)
			require.NoError(t, err)

			_, err = msentraid.New().EnrichUserWithUnixAttributes(
				context.Background(),
				info.NewUser("alice@example.com", "/home/alice", "user-id", "", "Alice", nil),
				"client-id",
				"https://issuer.example/tenant/v2.0",
				&oauth2.Token{AccessToken: accessTokenString},
				map[string]any{"msgraph_host": server.URL},
				nil,
				info.UnixAttributeConfig{UIDAttribute: "extension_uidNumber", UIDRequired: true},
			)
			require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
			require.ErrorIs(t, err, info.ErrUnixUIDRequired)
		})
	}
}

func TestEnrichUserWithUnixAttributesClearsNullGID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/me/transitiveMemberOf/graph.group", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"value":[{"id":"group-id","displayName":"Engineering","securityEnabled":true,"groupTypes":[],"extension_gidNumber":null}]}`)
	}))
	t.Cleanup(server.Close)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"scp": "GroupMember.Read.All User.Read"})
	accessTokenString, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err)
	previousGID := uint32(2001)
	got, err := msentraid.New().EnrichUserWithUnixAttributes(
		context.Background(),
		info.User{
			Name:   "alice@example.com",
			Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &previousGID}},
		},
		"client-id",
		"https://issuer.example/tenant/v2.0",
		&oauth2.Token{AccessToken: accessTokenString},
		map[string]any{"msgraph_host": server.URL},
		nil,
		info.UnixAttributeConfig{GIDAttribute: "extension_gidNumber"},
	)
	require.NoError(t, err)
	require.Len(t, got.Groups, 1)
	require.Nil(t, got.Groups[0].GID)
}

func TestNormalizeUsername(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		username string

		wantNormalized string
	}{
		"Shouldnt_change_all_lower_case": {
			username:       "name@email.com",
			wantNormalized: "name@email.com",
		},
		"Should_convert_all_to_lower_case": {
			username:       "NAME@email.com",
			wantNormalized: "name@email.com",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			ret := p.NormalizeUsername(tc.username)
			require.Equal(t, tc.wantNormalized, ret)
		})
	}
}

func TestVerifyUsername(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		requestedUsername string
		authenticatedUser string

		wantErr bool
	}{
		"Success_when_usernames_are_the_same":   {requestedUsername: "foo-bar@example", authenticatedUser: "foo-bar@example"},
		"Success_when_usernames_differ_in_case": {requestedUsername: "foo-bar@example", authenticatedUser: "Foo-Bar@example"},

		"Error_when_usernames_differ": {requestedUsername: "foo@example", authenticatedUser: "bar@foo", wantErr: true},
		"Error_when_requested_username_contains_invalid_characters": {
			requestedUsername: "fóó@example", authenticatedUser: "foo@example", wantErr: true,
		},
		"Error_when_authenticated_username_contains_invalid_characters": {
			requestedUsername: "foo@example", authenticatedUser: "fóó@example", wantErr: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()

			err := p.VerifyUsername(tc.requestedUsername, tc.authenticatedUser)
			if tc.wantErr {
				require.Error(t, err, "VerifyUsername should return an error")
				return
			}

			require.NoError(t, err, "VerifyUsername should not return an error")
		})
	}
}

func TestGetUserInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		IDToken *testIDToken

		wantErr bool
	}{
		"Successfully_get_user_info": {},

		"Error_when_id_token_is_missing_required_oid_claims": {IDToken: missingOIDClaimIDToken, wantErr: true},
		"Error_when_id_token_claims_are_invalid":             {IDToken: invalidIDToken, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idToken := validIDToken
			if tc.IDToken != nil {
				idToken = tc.IDToken
			}

			p := msentraid.New()

			got, err := p.GetUserInfo(idToken, false)
			if tc.wantErr {
				require.Error(t, err, "GetUserInfo should return an error")
				return
			}
			require.NoError(t, err, "GetUserInfo should not return an error")

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestUserInfoFromAccessToken(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid":  "saved-user-id",
		"upn":  "test-user@email.com",
		"name": "test-user",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	got, err := msentraid.New().UserInfoFromAccessToken(accessTokenStr)
	require.NoError(t, err, "UserInfoFromAccessToken should not return an error")
	require.Equal(t, info.NewUser("test-user@email.com", "", "saved-user-id", "", "test-user", nil), got)
}

func TestRefreshEntraToken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		refreshHandler http.HandlerFunc
		wantErr        bool
		wantErrSubstr  string
	}{
		"Active_user_refresh_succeeds": {},
		"Disabled_user_returns_AADSTS50057": {
			refreshHandler: disabledRefreshHandler,
			wantErr:        true,
			wantErrSubstr:  "AADSTS50057",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mockServer, cleanup := startMockMSServer(t, &mockMSServerConfig{RefreshHandler: tc.refreshHandler})
			t.Cleanup(cleanup)

			got, err := msentraid.New().RefreshEntraToken(
				context.Background(),
				mockServer.URL+"/tenant-id/v2.0",
				"refreshtoken",
			)
			if tc.wantErr {
				require.Error(t, err, "RefreshEntraToken should fail")
				require.Contains(t, err.Error(), tc.wantErrSubstr, "unexpected error from refresh")
				return
			}
			require.NoError(t, err, "RefreshEntraToken should succeed for an active user")
			require.NotEmpty(t, got.AccessToken, "expected a rotated token on success")
			require.Nil(t, got.Extra("preferred_username"), "refresh should not add redundant preferred_username extras")
			require.Nil(t, got.Extra("sub"), "refresh should not add redundant sub extras")
			require.Nil(t, got.Extra("name"), "refresh should not add redundant name extras")
		})
	}
}

func TestGetGroups(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")
	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	tests := map[string]struct {
		tokenScopes        []string
		providerMetadata   map[string]any
		acquireAccessToken bool

		groupEndpointHandler http.HandlerFunc

		wantErr bool
	}{
		"Successfully_get_groups":                               {},
		"Successfully_get_groups_with_local_groups":             {groupEndpointHandler: localGroupHandler},
		"Successfully_get_groups_with_mixed_groups":             {groupEndpointHandler: mixedGroupHandler},
		"Successfully_get_groups_filtering_non_security_groups": {groupEndpointHandler: nonSecurityGroupHandler},
		"Successfully_get_groups_with_acquired_access_token":    {acquireAccessToken: true},

		"Error_when_msgraph_host_is_invalid":             {providerMetadata: map[string]any{"msgraph_host": "invalid"}, wantErr: true},
		"Error_when_token_does_not_have_required_scopes": {tokenScopes: []string{"not the required scopes"}, wantErr: true},
		"Error_when_getting_user_groups_fails":           {groupEndpointHandler: errorGroupHandler, wantErr: true},
		"Error_when_group_is_missing_id":                 {groupEndpointHandler: missingIDGroupHandler, wantErr: true},
		"Error_when_group_is_missing_display_name":       {groupEndpointHandler: missingDisplayNameGroupHandler, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.tokenScopes == nil {
				tc.tokenScopes = strings.Split(msentraid.AllExpectedScopes(), " ")
			}

			if tc.providerMetadata == nil {
				mockServer, cleanup := startMockMSServer(t, &mockMSServerConfig{
					GroupEndpointHandler: tc.groupEndpointHandler,
				})
				t.Cleanup(cleanup)
				tc.providerMetadata = map[string]any{"msgraph_host": mockServer.URL}
			}

			var deviceRegistrationData []byte
			if tc.acquireAccessToken {
				var cleanup func()
				deviceRegistrationData, cleanup, err = maybeRegisterDevice(t, nil)
				t.Cleanup(cleanup)
				require.NoError(t, err, "maybeRegisterDevice should not return an error")
			}

			p := msentraid.New()
			p.SetTokenScopesForGraphAPI(tc.tokenScopes)

			got, err := p.GetGroups(
				context.Background(),
				"",
				"",
				token,
				tc.providerMetadata,
				deviceRegistrationData,
				tc.acquireAccessToken,
			)
			if tc.wantErr {
				require.Error(t, err, "GetUserInfo should return an error")
				return
			}
			require.NoError(t, err, "GetUserInfo should not return an error")

			golden.CheckOrUpdateYAML(t, got)
		})
	}
}

func TestGetGroupsUsesCurrentTokenWhenAlreadyGraphCapable(t *testing.T) {
	t.Parallel()

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"scp": "GroupMember.Read.All User.Read",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	p := msentraid.New()

	got, err := p.GetGroups(
		context.Background(),
		"",
		"",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		true,
	)
	require.NoError(t, err, "GetGroups should use the current token when it already has Graph scopes")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestGetGroupsUsesClientCredentialsFallback(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": "00000000-0000-0000-0000-000000000000",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	got, err := p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		false,
	)
	require.NoError(t, err, "GetGroups should fall back to client credentials when the delegated token lacks Graph scope")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestGetGroupsDeviceRegistrationTokenDoesNotUseClientCredentials(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": "00000000-0000-0000-0000-000000000000",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	// needsAccessTokenForGraphAPI=true marks a device-registration token, which
	// must be exchanged via the PRT path (strategy 2) rather than the app-only
	// client-credentials path, even when a client secret is configured. With no
	// device registration data the PRT path fails, but it must NOT silently fall
	// through to client credentials (which would otherwise succeed here).
	_, err = p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		true,
	)
	require.Error(t, err, "GetGroups must not use client credentials for a device-registration token")
	require.Contains(t, err.Error(), "device registration",
		"GetGroups should fail on the device-registration token-exchange path, not client credentials")
}

func TestGetGroupsInvalidTokenWithClientCredentialsReturnsError(t *testing.T) {
	t.Parallel()

	mockServer, cleanup := startMockMSServer(t, nil)
	t.Cleanup(cleanup)

	token := &oauth2.Token{AccessToken: "invalid-token"}

	p := msentraid.New()
	p.SetGraphClientSecret("client-secret")

	_, err := p.GetGroups(
		context.Background(),
		"client-id",
		mockServer.URL+"/tenant-id/v2.0",
		token,
		map[string]any{"msgraph_host": mockServer.URL},
		nil,
		false,
	)
	require.Error(t, err, "GetGroups should return an error instead of panicking on invalid delegated tokens")
}

func TestGetGroupsClientCredentialsUsesConfiguredIssuerAndGraphHosts(t *testing.T) {
	t.Parallel()

	const (
		clientID     = "client-id"
		clientSecret = "client-secret"
		tenantID     = "tenant-id"
		userOID      = "user-oid"
	)

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"oid": userOID,
		"scp": "User.Read",
	})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")

	var tokenEndpointCalled atomic.Bool
	var graphEndpointCalled atomic.Bool
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/"+tenantID+"/oauth2/v2.0/token":
			tokenEndpointCalled.Store(true)
			require.NoError(t, r.ParseForm(), "failed to parse client credentials form")
			require.Equal(t, "client_credentials", r.Form.Get("grant_type"))
			require.Equal(t, clientID, r.Form.Get("client_id"))
			require.Equal(t, clientSecret, r.Form.Get("client_secret"))
			require.Equal(t, mockServer.URL+"/.default", r.Form.Get("scope"))

			appToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
			appTokenStr, err := appToken.SignedString(testutils.MockKey)
			require.NoError(t, err, "failed to sign app token")

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, appTokenStr)

		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/users/"+userOID+"/") &&
			strings.Contains(r.URL.Path, "/transitiveMemberOf/") &&
			strings.HasSuffix(r.URL.Path, "graph.group"):
			graphEndpointCalled.Store(true)
			simpleGroupHandler(w, r)

		default:
			require.Fail(t, "unexpected request", "method=%s path=%s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(mockServer.Close)

	p := msentraid.New()
	p.SetGraphClientSecret(clientSecret)

	got, err := p.GetGroups(
		context.Background(),
		clientID,
		fmt.Sprintf("%s/%s/v2.0", mockServer.URL, tenantID),
		&oauth2.Token{AccessToken: accessTokenStr},
		map[string]any{"msgraph_host": mockServer.URL + "/v1.0"},
		nil,
		false,
	)
	require.NoError(t, err, "GetGroups should use client credentials against configured hosts")
	require.True(t, tokenEndpointCalled.Load(), "client credentials token endpoint should have been called")
	require.True(t, graphEndpointCalled.Load(), "Graph users endpoint should have been called")
	require.ElementsMatch(t, []info.Group{
		{Name: "group1", UGID: "id1"},
		{Name: "group2", UGID: "id2"},
	}, got)
}

func TestIsTokenForDeviceRegistration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deviceRegistrationData []byte

		want bool
	}{
		"True_when_device_registration_data_is_present": {deviceRegistrationData: []byte("device-registration-data"), want: true},
		"False_when_device_registration_data_is_absent": {deviceRegistrationData: nil, want: false},
		"False_when_device_registration_data_is_empty":  {deviceRegistrationData: []byte{}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			got := p.IsTokenForDeviceRegistration(&token.AuthCachedInfo{DeviceRegistrationData: tc.deviceRegistrationData})

			require.Equal(t, tc.want, got, "IsTokenForDeviceRegistration should return the expected value")
		})
	}
}

func TestMaybeRegisterDevice(t *testing.T) {
	t.Parallel()

	registrationData, err := json.Marshal(&himmelblau.DeviceRegistrationData{
		DeviceID:      "test-device-id",
		CertKey:       []byte("test-cert-key"),
		TransportKey:  []byte("test-transport-key"),
		AuthValue:     "test-auth-value",
		TPMMachineKey: []byte("test-tpm-machine-key"),
	})
	require.NoError(t, err, "Failed to marshal device registration data")

	type args = maybeRegisterDeviceArgs

	tests := map[string]struct {
		args

		wantErr bool
	}{
		"Successfully_registers_device":       {},
		"Reuses_existing_device_registration": {args: args{oldData: registrationData}},

		"Error_when_username_does_not_have_a_domain": {args: args{username: "userwithoutdomain"}, wantErr: true},
		"Error_when_discover_url_is_invalid_format":  {args: args{discoveryURL: "invalid-url"}, wantErr: true},
		"Error_when_discover_url_is_unreachable":     {args: args{discoveryURL: "http://invalid-url"}, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registrationData, cleanup, err := maybeRegisterDevice(t, &tc.args)
			t.Cleanup(cleanup)
			if tc.wantErr {
				require.Error(t, err, "MaybeRegisterDevice should return an error")
				return
			}
			require.NoError(t, err, "MaybeRegisterDevice should not return an error")

			if tc.oldData != nil {
				require.Equal(t, tc.oldData, registrationData, "MaybeRegisterDevice should return the existing registration data")
			}

			// We don't compare the registration data with a golden file, because it differs every time due to the
			// generated keys. Instead, we just check that it's not empty.
			require.NotEmpty(t, registrationData, "MaybeRegisterDevice should return non-empty registration data")
		})
	}
}

type maybeRegisterDeviceArgs struct {
	username     string
	oldData      []byte
	discoveryURL string
}

func maybeRegisterDevice(
	t *testing.T,
	args *maybeRegisterDeviceArgs,
) ([]byte, func(), error) {
	// Start the mock MS server (or reuse the existing one)
	ensureMockMSServerForDeviceRegistration(t)
	mockServer := mockMSServerForDeviceRegistration

	if args == nil {
		args = &maybeRegisterDeviceArgs{}
	}

	if args.discoveryURL == "" {
		args.discoveryURL = mockServer.URL
	}

	if args.username == "" {
		args.username = "user@example.com"
	}

	// Make libhimmelblau use the mock MS server. These settings are global,
	// so test case which need different settings must not run in parallel.
	if args.discoveryURL == "" {
		// We don't need to set the environment variable, just ensure no other test is modifying it while we run.
		discoveryURLMu.RLock()
		defer discoveryURLMu.RUnlock()
	} else {
		// Set the environment variable for the duration of the test.
		discoveryURLMu.Lock()
		oldValue := os.Getenv("HIMMELBLAU_DISCOVERY_URL")
		err := os.Setenv("HIMMELBLAU_DISCOVERY_URL", args.discoveryURL)
		require.NoError(t, err, "Failed to set HIMMELBLAU_DISCOVERY_URL environment variable")
		defer func() {
			err := os.Setenv("HIMMELBLAU_DISCOVERY_URL", oldValue)
			discoveryURLMu.Unlock()
			require.NoError(t, err, "Failed to unset HIMMELBLAU_DISCOVERY_URL environment variable")
		}()
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{})
	accessTokenStr, err := accessToken.SignedString(testutils.MockKey)
	require.NoError(t, err, "Failed to sign access token")
	token := &oauth2.Token{
		AccessToken:  accessTokenStr,
		RefreshToken: "refreshtoken",
		Expiry:       time.Now().Add(1000 * time.Hour),
	}

	tenantID := "8de88d99-6d0f-44d7-a8a5-925b012e5940"
	issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)

	p := msentraid.New()

	return p.MaybeRegisterDevice(
		context.Background(),
		token,
		args.username,
		issuerURL,
		args.oldData,
	)
}

func TestIsTokenExpiredError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		errorCode        string
		errorDescription string

		wantExpired bool
	}{
		"AADSTS50078_mfa_session_expired":              {errorCode: "invalid_grant", errorDescription: "AADSTS50078: Presented multi-factor authentication has expired due to policies configured by your administrator.", wantExpired: true},
		"AADSTS50089_flow_token_expired":               {errorCode: "invalid_grant", errorDescription: "AADSTS50089: Flow token has expired. User needs to reauthenticate.", wantExpired: true},
		"AADSTS50173_token_expired":                    {errorCode: "invalid_grant", errorDescription: "AADSTS50173: The provided grant has expired", wantExpired: true},
		"AADSTS70008_token_expired_due_to_inactivity":  {errorCode: "invalid_grant", errorDescription: "AADSTS70008: The refresh token has expired due to inactivity.", wantExpired: true},
		"AADSTS70043_token_expired":                    {errorCode: "invalid_grant", errorDescription: "AADSTS70043: The refresh token has expired or is invalid", wantExpired: true},
		"AADSTS700082_token_expired_due_to_inactivity": {errorCode: "invalid_grant", errorDescription: "AADSTS700082: The refresh token has expired due to inactivity.", wantExpired: true},

		"AADSTS50057_user_disabled": {errorCode: "invalid_grant", errorDescription: "AADSTS50057: The user account is disabled.", wantExpired: false},
		"Other_invalid_grant":       {errorCode: "invalid_grant", errorDescription: "AADSTS65001: The user or administrator has not consented to use the application.", wantExpired: false},
		"Non_invalid_grant_error":   {errorCode: "access_denied", errorDescription: "AADSTS50173: The provided grant has expired", wantExpired: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := msentraid.New()
			err := &oauth2.RetrieveError{
				ErrorCode:        tc.errorCode,
				ErrorDescription: tc.errorDescription,
			}
			got := p.IsTokenExpiredError(err)
			require.Equal(t, tc.wantExpired, got, "IsTokenExpiredError returned unexpected result")
		})
	}
}

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)

	m.Run()
}
