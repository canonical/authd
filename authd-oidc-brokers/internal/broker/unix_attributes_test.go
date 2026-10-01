package broker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/authd-oidc-brokers/internal/providers/info"
	"github.com/canonical/authd/authd-oidc-brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
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

func TestDropStaleUnixIDs(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	current := &token.UnixAttributeNames{UID: "extension_uidNumber", GID: "extension_gidNumber"}
	tests := []struct {
		name         string
		uidAttribute string
		gidAttribute string
		names        *token.UnixAttributeNames
		uid          *uint32
		gid          *uint32
		wantUID      *uint32
		wantGID      *uint32
		wantChanged  bool
	}{
		{
			name:         "matching names keep IDs",
			uidAttribute: "extension_uidNumber",
			gidAttribute: "extension_gidNumber",
			names:        current,
			uid:          &uid,
			gid:          &gid,
			wantUID:      &uid,
			wantGID:      &gid,
		},
		{
			name:         "renamed UID attribute drops only the UID",
			uidAttribute: "extension_newUidNumber",
			gidAttribute: "extension_gidNumber",
			names:        current,
			uid:          &uid,
			gid:          &gid,
			wantGID:      &gid,
			wantChanged:  true,
		},
		{
			name:         "renamed GID attribute drops only the GIDs",
			uidAttribute: "extension_uidNumber",
			gidAttribute: "extension_newGidNumber",
			names:        current,
			uid:          &uid,
			gid:          &gid,
			wantUID:      &uid,
			wantChanged:  true,
		},
		{
			name:         "missing names drop all IDs",
			uidAttribute: "extension_uidNumber",
			gidAttribute: "extension_gidNumber",
			uid:          &uid,
			gid:          &gid,
			wantChanged:  true,
		},
		{
			name:        "removed attributes drop all IDs",
			names:       current,
			uid:         &uid,
			gid:         &gid,
			wantChanged: true,
		},
		{
			name:         "snapshot without IDs is not changed",
			uidAttribute: "extension_newUidNumber",
			gidAttribute: "extension_newGidNumber",
			names:        current,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			broker := &Broker{cfg: Config{userConfig: userConfig{
				unixUIDAttribute: tc.uidAttribute,
				unixGIDAttribute: tc.gidAttribute,
			}}}
			authInfo := &token.AuthCachedInfo{
				UnixAttributeNames: tc.names,
				UserInfo: info.User{
					Name:   "alice",
					UID:    tc.uid,
					Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: tc.gid}, {Name: "sudo"}},
				},
			}

			changed := broker.dropStaleUnixIDs(authInfo)

			require.Equal(t, tc.wantChanged, changed)
			require.Equal(t, tc.wantUID, authInfo.UserInfo.UID)
			require.Equal(t, []info.Group{{Name: "engineering", UGID: "group-id", GID: tc.wantGID}, {Name: "sudo"}}, authInfo.UserInfo.Groups)
		})
	}
}

func TestLoadCachedAuthInfoRewritesStaleUnixIDs(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	broker := &Broker{cfg: Config{userConfig: userConfig{
		unixUIDAttribute: "extension_newUidNumber",
		unixGIDAttribute: "extension_gidNumber",
	}}}
	tokenPath := filepath.Join(t.TempDir(), "token.json")
	require.NoError(t, token.CacheAuthInfo(tokenPath, &token.AuthCachedInfo{
		Token:              &oauth2.Token{AccessToken: "access-token"},
		UnixAttributeNames: &token.UnixAttributeNames{UID: "extension_uidNumber", GID: "extension_gidNumber"},
		UserInfo: info.User{
			Name:   "alice",
			UID:    &uid,
			Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}},
		},
	}))

	authInfo, err := broker.loadCachedAuthInfo(&session{tokenPath: tokenPath}, tokenPath)
	require.NoError(t, err)
	require.Nil(t, authInfo.UserInfo.UID)
	require.Equal(t, uint32(2001), *authInfo.UserInfo.Groups[0].GID)

	stored, err := token.LoadAuthInfo(tokenPath)
	require.NoError(t, err)
	require.Nil(t, stored.UserInfo.UID)
	require.Equal(t, uint32(2001), *stored.UserInfo.Groups[0].GID)
}

func TestCacheCredentialsMarksNewEnrichmentCheckpointIncomplete(t *testing.T) {
	t.Parallel()

	broker := &Broker{cfg: Config{userConfig: userConfig{
		unixUIDAttribute: "extension_uidNumber",
	}}}
	tokenPath := filepath.Join(t.TempDir(), "token.json")
	err := broker.cacheCredentialsWithPreviousUserInfo(
		&session{tokenPath: tokenPath},
		&token.AuthCachedInfo{
			Token:    &oauth2.Token{AccessToken: "access-token"},
			UserInfo: info.User{Name: "alice"},
		},
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
