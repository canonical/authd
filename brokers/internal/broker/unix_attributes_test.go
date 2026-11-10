package broker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/authd/brokers/internal/providers/info"
	"github.com/canonical/authd/brokers/internal/testutils"
	"github.com/canonical/authd/brokers/internal/token"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type unixAttributeTestProvider struct {
	*testutils.MockProvider
	groups       info.GroupFetchResult
	groupsErr    error
	user         info.User
	enrichErr    error
	beforeEnrich func()
}

func (p *unixAttributeTestProvider) GetGroups(_ context.Context, _, _ string, _ *oauth2.Token, _ map[string]interface{}, _ []byte, _ bool, _ string) (info.GroupFetchResult, error) {
	return p.groups, p.groupsErr
}

func (p *unixAttributeTestProvider) EnrichUserWithUnixAttributes(_ context.Context, _ info.User, _ info.GroupFetchResult, _, _ string, _ *oauth2.Token, _ map[string]interface{}, _ []byte, _ info.UnixAttributeConfig) (info.User, error) {
	if p.beforeEnrich != nil {
		p.beforeEnrich()
	}
	return p.user, p.enrichErr
}

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

func TestEnrichUserInfoOfflineUsesCachedUnixIDs(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	gid := uint32(2001)
	tests := []struct {
		name   string
		config Config
		user   info.User
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
			name: "required UID may be missing",
			config: Config{userConfig: userConfig{
				unixUIDAttribute: "extension_uidNumber",
				unixUIDRequired:  true,
			}},
			user: info.User{},
		},
		{
			name: "required remote GID may be missing",
			config: Config{userConfig: userConfig{
				unixGIDAttribute: "extension_gidNumber",
				unixGIDRequired:  true,
			}},
			user: info.User{Groups: []info.Group{{Name: "engineering", UGID: "group-id"}}},
		},
		{
			name: "complete snapshot is kept",
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
			require.NoError(t, broker.enrichUserInfo(context.Background(), &session{isOffline: true}, authInfo, nil))
			require.Equal(t, tc.user, authInfo.UserInfo)
		})
	}
}

func TestEnrichUserInfoHandlesUnixAttributeErrors(t *testing.T) {
	t.Parallel()

	graphErr := errors.New("Graph request failed")
	enriched := info.User{Name: "alice", Groups: []info.Group{{Name: "engineering"}}}
	tests := map[string]struct {
		groupsErr    error
		enrichErr    error
		forceCheck   bool
		required     bool
		wantErr      error
		wantRequired bool
		wantEnriched bool
	}{
		"required group lookup failure": {
			groupsErr:    graphErr,
			forceCheck:   true,
			required:     true,
			wantErr:      info.ErrGroupsUnavailable,
			wantRequired: true,
		},
		"required attribute lookup failure": {
			enrichErr:    graphErr,
			forceCheck:   true,
			required:     true,
			wantErr:      info.ErrGroupsUnavailable,
			wantRequired: true,
		},
		"optional attribute lookup failure": {
			enrichErr: graphErr,
			wantErr:   info.ErrGroupsUnavailable,
		},
		"missing attribute is always reported": {
			enrichErr:    errors.Join(info.ErrUnixAttributePartial, info.ErrUnixAttributeMissing),
			wantErr:      info.ErrUnixAttributePartial,
			wantEnriched: true,
		},
		"unreadable optional attribute keeps cached fallback": {
			enrichErr:    errors.Join(info.ErrUnixAttributePartial, graphErr),
			wantEnriched: true,
		},
		"forced unreadable attribute is reported": {
			enrichErr:    errors.Join(info.ErrUnixAttributePartial, graphErr),
			forceCheck:   true,
			wantErr:      info.ErrUnixAttributePartial,
			wantEnriched: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &unixAttributeTestProvider{
				MockProvider: &testutils.MockProvider{},
				groups:       info.GroupFetchResult{},
				groupsErr:    tc.groupsErr,
				user:         enriched,
				enrichErr:    tc.enrichErr,
			}
			broker := &Broker{
				cfg: Config{userConfig: userConfig{
					unixUIDAttribute:             "extension_uidNumber",
					unixUIDRequired:              tc.required,
					forceAccessCheckWithProvider: tc.forceCheck,
				}},
				provider: provider,
			}
			authInfo := &token.AuthCachedInfo{UserInfo: info.User{Name: "alice"}}

			err := broker.enrichUserInfo(context.Background(), &session{}, authInfo, nil)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tc.wantRequired {
				require.ErrorIs(t, err, info.ErrUnixAttributeRequired)
			}
			if tc.wantEnriched {
				require.Equal(t, enriched, authInfo.UserInfo)
				require.Equal(t, &token.UnixAttributeNames{UID: "extension_uidNumber"}, authInfo.UnixAttributeNames)
			} else {
				require.Equal(t, info.User{Name: "alice"}, authInfo.UserInfo)
			}
		})
	}
}

func TestFetchUnixAttributesRequiresEnricher(t *testing.T) {
	t.Parallel()

	broker := &Broker{provider: &testutils.MockProvider{}}
	_, err := broker.fetchUnixAttributes(context.Background(), &token.AuthCachedInfo{}, nil, info.GroupFetchResult{})
	require.ErrorContains(t, err, "provider does not support Unix attribute enrichment")
}

func TestRefreshGroupsOrEnrichReportsCacheWriteErrors(t *testing.T) {
	t.Parallel()

	t.Run("preserving refreshed credentials", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		obstruction := filepath.Join(dir, "not-a-directory")
		require.NoError(t, os.WriteFile(obstruction, nil, 0600))
		broker := &Broker{cfg: Config{userConfig: userConfig{
			unixUIDAttribute: "extension_uidNumber",
		}}}

		err := broker.refreshGroupsOrEnrich(
			context.Background(),
			&session{tokenPath: filepath.Join(obstruction, "token.json")},
			&token.AuthCachedInfo{},
			nil,
			&token.AuthCachedInfo{},
		)
		require.ErrorContains(t, err, "could not preserve refreshed credentials")
	})

	t.Run("storing partial enrichment", func(t *testing.T) {
		for _, returningUser := range []bool{false, true} {
			t.Run(fmt.Sprintf("returning=%t", returningUser), func(t *testing.T) {
				t.Parallel()

				tokenPath := filepath.Join(t.TempDir(), "token.json")
				provider := &unixAttributeTestProvider{
					MockProvider: &testutils.MockProvider{},
					user:         info.User{Name: "alice"},
					enrichErr:    errors.Join(info.ErrUnixAttributePartial, errors.New("Graph request failed")),
					beforeEnrich: func() {
						if _, err := os.Stat(tokenPath); err == nil {
							require.NoError(t, os.Remove(tokenPath))
						} else {
							require.ErrorIs(t, err, os.ErrNotExist)
						}
						require.NoError(t, os.Mkdir(tokenPath, 0700))
					},
				}
				broker := &Broker{
					cfg: Config{userConfig: userConfig{
						unixUIDAttribute:             "extension_uidNumber",
						forceAccessCheckWithProvider: true,
					}},
					provider: provider,
				}
				authInfo := &token.AuthCachedInfo{UserInfo: info.User{Name: "alice"}}
				if !returningUser {
					authInfo.DeviceRegistrationDataObtainedAt = 1
				}
				var oldAuthInfo *token.AuthCachedInfo
				if returningUser {
					oldAuthInfo = &token.AuthCachedInfo{UserInfo: info.User{Name: "alice"}}
				}

				err := broker.refreshGroupsOrEnrich(context.Background(), &session{tokenPath: tokenPath}, authInfo, nil, oldAuthInfo)
				require.ErrorIs(t, err, info.ErrUnixAttributePartial)
				if returningUser {
					require.ErrorContains(t, err, "could not store partial Unix ID data")
				} else {
					require.ErrorContains(t, err, "could not store registration state")
				}
			})
		}
	})
}

func TestPreserveCachedUserInfoCopiesGroupsAndNames(t *testing.T) {
	t.Parallel()

	uid := uint32(1001)
	names := &token.UnixAttributeNames{UID: "extension_uidNumber", GID: "extension_gidNumber"}
	oldAuthInfo := &token.AuthCachedInfo{
		UnixAttributeNames: names,
		UserInfo: info.User{
			Name:   "alice",
			UID:    &uid,
			Groups: []info.Group{{Name: "engineering", UGID: "group-id"}},
		},
	}
	authInfo := &token.AuthCachedInfo{UserInfo: info.User{Name: "alice"}}

	(&Broker{}).preserveCachedUserInfo(authInfo, oldAuthInfo)

	require.Equal(t, oldAuthInfo.UserInfo, authInfo.UserInfo)
	require.NotSame(t, &oldAuthInfo.UserInfo.Groups[0], &authInfo.UserInfo.Groups[0])
	require.Equal(t, names, authInfo.UnixAttributeNames)
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

func TestLoadCachedAuthInfoStaleIDRewriteFailure(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("read-only file permissions do not block root")
	}

	tests := map[string]struct {
		forceAccessCheck bool
		uidRequired      bool
		gidRequired      bool

		wantLoginDenied bool
	}{
		"Deny_login_when_forced_check_requires_UID":     {forceAccessCheck: true, uidRequired: true, wantLoginDenied: true},
		"Deny_login_when_forced_check_requires_GID":     {forceAccessCheck: true, gidRequired: true, wantLoginDenied: true},
		"Allow_login_when_forced_check_requires_no_ID":  {forceAccessCheck: true},
		"Allow_login_when_no_forced_check_requires_UID": {uidRequired: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			uid := uint32(1001)
			gid := uint32(2001)
			broker := &Broker{cfg: Config{userConfig: userConfig{
				unixUIDAttribute:             "extension_newUidNumber",
				unixGIDAttribute:             "extension_gidNumber",
				forceAccessCheckWithProvider: tc.forceAccessCheck,
				unixUIDRequired:              tc.uidRequired,
				unixGIDRequired:              tc.gidRequired,
			}}}
			dir := filepath.Join(t.TempDir(), "provider-id")
			tokenPath := filepath.Join(dir, "token.json")
			require.NoError(t, token.CacheAuthInfo(tokenPath, &token.AuthCachedInfo{
				Token:              &oauth2.Token{AccessToken: "access-token"},
				UnixAttributeNames: &token.UnixAttributeNames{UID: "extension_uidNumber", GID: "extension_gidNumber"},
				UserInfo: info.User{
					Name:   "alice",
					UID:    &uid,
					Groups: []info.Group{{Name: "engineering", UGID: "group-id", GID: &gid}},
				},
			}))
			require.NoError(t, os.Chmod(tokenPath, 0400), "Setup: making the cache file read-only should not fail")

			authInfo, err := broker.loadCachedAuthInfo(&session{tokenPath: tokenPath}, tokenPath)
			if tc.wantLoginDenied {
				require.Error(t, err, "loading should fail when the stale ID cannot be removed")
				stored, loadErr := token.LoadAuthInfo(tokenPath)
				require.NoError(t, loadErr)
				require.NotNil(t, stored.UserInfo.UID, "the stale UID should stay in the cache")
				return
			}
			require.NoError(t, err)
			require.Nil(t, authInfo.UserInfo.UID, "the stale UID should be dropped in memory")
		})
	}
}

func TestCacheCredentialsWithoutPreviousCacheKeepsUserInfo(t *testing.T) {
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
	require.Equal(t, "alice", cached.UserInfo.Name)
	require.Nil(t, cached.UnixAttributeNames)
}

func TestErrorMessageForDisplayReportsRequiredUID(t *testing.T) {
	t.Parallel()

	got := errorMessageForDisplay(info.ErrUnixUIDRequired, "fallback")
	require.Equal(t, "Authentication failure: required Unix UID for your account not provided.", got.Message)
}

func TestErrorMessageForDisplayReportsRequiredGID(t *testing.T) {
	t.Parallel()

	got := errorMessageForDisplay(info.ErrUnixGIDRequired, "fallback")
	require.Equal(t, "Authentication failure: required Unix GID for one or more of your groups not provided.", got.Message)
}
