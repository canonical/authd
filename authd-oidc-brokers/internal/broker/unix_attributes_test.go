package broker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/stretchr/testify/require"
)

func TestUserInfoForResponseDoesNotMutateCachedUnixIDs(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	cached := info.User{
		Name:   "alice",
		UID:    &uid,
		Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}},
	}

	response := userInfoForResponse(cached)

	require.Nil(t, response.UID)
	require.Len(t, response.Groups, 1)
	require.Nil(t, response.Groups[0].GID)
	require.NotSame(t, &cached.Groups[0], &response.Groups[0])
	require.Equal(t, uint32(1001), *cached.UID)
	require.Equal(t, uint32(2001), *cached.Groups[0].GID)
}

func TestEnrichUserInfoOfflineValidatesCachedUnixIDs(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	tests := []struct {
		name       string
		config     Config
		user       info.User
		wantErr    bool
		wantUIDErr bool
		wantGIDErr bool
	}{
		{
			name: "optional IDs may be missing",
			config: Config{userConfig: userConfig{
				unixUIDAttribute: "extension_uidNumber",
				unixGIDAttribute: "extension_gidNumber",
			}},
			user: info.User{Groups: []info.Group{{Name: "engineering", UGID: "group-id"}}},
		},
		{
			name: "required UID must be cached",
			config: Config{userConfig: userConfig{
				unixUIDAttribute: "extension_uidNumber",
				unixUIDRequired:  true,
			}},
			user:       info.User{},
			wantErr:    true,
			wantUIDErr: true,
		},
		{
			name: "required remote GID must be cached",
			config: Config{userConfig: userConfig{
				unixGIDAttribute: "extension_gidNumber",
				unixGIDRequired:  true,
			}},
			user:       info.User{Groups: []info.Group{{Name: "engineering", UGID: "group-id"}}},
			wantErr:    true,
			wantGIDErr: true,
		},
		{
			name: "required GID ignores local groups",
			config: Config{userConfig: userConfig{
				unixGIDAttribute: "extension_gidNumber",
				unixGIDRequired:  true,
			}},
			user: info.User{Groups: []info.Group{{Name: "sudo"}}},
		},
		{
			name: "complete required snapshot is accepted",
			config: Config{userConfig: userConfig{
				unixUIDAttribute: "extension_uidNumber",
				unixGIDAttribute: "extension_gidNumber",
				unixUIDRequired:  true,
				unixGIDRequired:  true,
			}},
			user: info.User{
				UID:    &uid,
				Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			broker := &Broker{cfg: tc.config}
			authInfo := &token.AuthCachedInfo{UserInfo: tc.user}
			err := broker.enrichUserInfo(context.Background(), &session{isOffline: true}, authInfo, nil)
			if tc.wantErr {
				require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
				if tc.wantUIDErr {
					require.ErrorIs(t, err, info.ErrUnixUIDRequired)
				}
				if tc.wantGIDErr {
					require.ErrorIs(t, err, info.ErrUnixGIDRequired)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCachedUnixAttributeSnapshotState(t *testing.T) {
	t.Parallel()

	incomplete := false
	complete := true
	broker := &Broker{cfg: Config{userConfig: userConfig{
		unixUIDAttribute: "extension_uidNumber",
		unixGIDAttribute: "extension_gidNumber",
	}}}
	for _, tc := range []struct {
		name      string
		marker    *bool
		wantError bool
	}{
		{name: "new incomplete checkpoint", marker: &incomplete, wantError: true},
		{name: "legacy cache without marker"},
		{name: "completed cache", marker: &complete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldAuthInfo := &token.AuthCachedInfo{UnixAttributesEnriched: tc.marker}
			err := broker.restoreCachedUserInfo(&token.AuthCachedInfo{}, oldAuthInfo)
			if tc.wantError {
				require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCacheCredentialsMarksNewEnrichmentCheckpointIncomplete(t *testing.T) {
	t.Parallel()

	broker := &Broker{cfg: Config{userConfig: userConfig{
		unixUIDAttribute: "extension_uidNumber",
	}}}
	tokenPath := filepath.Join(t.TempDir(), "token.json")
	err := broker.cacheCredentialsWithPreviousUserInfo(
		&session{tokenPath: tokenPath},
		&token.AuthCachedInfo{UserInfo: info.User{Name: "alice"}},
		nil,
	)
	require.NoError(t, err)

	cached, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.NotNil(t, cached.UnixAttributesEnriched)
	require.False(t, *cached.UnixAttributesEnriched)
}

func TestErrorMessageForDisplayReportsRequiredUID(t *testing.T) {
	t.Parallel()

	got := errorMessageForDisplay(info.ErrUnixUIDRequired, "fallback")
	require.Equal(t, "Microsoft Entra ID did not provide a valid required Unix UID for your account. Please contact your administrator.", got.Message)
}

func TestErrorMessageForDisplayReportsRequiredGID(t *testing.T) {
	t.Parallel()

	got := errorMessageForDisplay(info.ErrUnixGIDRequired, "fallback")
	require.Equal(t, "Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.", got.Message)
}
