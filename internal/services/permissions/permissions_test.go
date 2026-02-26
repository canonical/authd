package permissions_test

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/canonical/authd/internal/services/permissions"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func TestNew(t *testing.T) {
	t.Parallel()

	pm := permissions.New()

	require.NotNil(t, pm, "New permission manager is created")
}

func TestIsRequestFromRoot(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		currentUserNotRoot bool
		noPeerCredsInfo    bool
		noAuthInfo         bool

		wantErr bool
	}{
		"Granted_if_current_user_considered_as_root": {},

		"Error_as_deny_when_current_user_is_not_root": {currentUserNotRoot: true, wantErr: true},
		"Error_as_deny_when_missing_peer_creds_Info":  {noPeerCredsInfo: true, wantErr: true},
		"Error_as_deny_when_missing_auth_info_creds":  {noAuthInfo: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Setup peer creds info
			ctx := context.Background()
			if !tc.noPeerCredsInfo {
				var authInfo credentials.AuthInfo
				if !tc.noAuthInfo {
					uid := permissions.CurrentUserUID()
					pid := os.Getpid()
					if pid > math.MaxInt32 {
						t.Fatalf("Setup: pid is too large to be converted to int32: %d", pid)
					}
					//nolint:gosec // we did check the conversion check beforehand.
					authInfo = permissions.NewTestPeerCredsInfo(uid, int32(os.Getpid()))
				}
				p := peer.Peer{
					AuthInfo: authInfo,
				}
				ctx = peer.NewContext(ctx, &p)
			}

			var opts []permissions.Option
			if !tc.currentUserNotRoot {
				opts = append(opts, permissions.Z_ForTests_WithCurrentUserAsRoot())
			}
			pm := permissions.New(opts...)

			err := pm.CheckRequestIsFromRoot(ctx)

			if tc.wantErr {
				require.Error(t, err, "CheckRequestIsFromRoot should deny access but didn't")
				return
			}
			require.NoError(t, err, "CheckRequestIsFromRoot should allow access but didn't")
		})
	}
}

func TestWithUnixPeerCreds(t *testing.T) {
	t.Parallel()

	g := grpc.NewServer(permissions.WithUnixPeerCreds())

	require.NotNil(t, g, "New gRPC with Unix Peer Creds is created")
}
