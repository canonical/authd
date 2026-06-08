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

func TestCheckRequestIsFromRoot(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		currentUserNotRoot bool
		noPeerInfo         bool
		noPeerAuthInfo     bool

		wantErr bool
	}{
		"Granted_if_current_user_considered_as_root": {},

		"Error_if_current_user_is_not_root": {currentUserNotRoot: true, wantErr: true},
		"Error_if_missing_peer_info":        {noPeerInfo: true, wantErr: true},
		"Error_if_missing_peer_auth_info":   {noPeerAuthInfo: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := setupPermissionTestContext(t, tc.noPeerInfo, tc.noPeerAuthInfo)

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

// setupPermissionTestContext creates a context with peer credentials for testing.
func setupPermissionTestContext(t *testing.T, noPeerInfo, noAuthInfo bool) context.Context {
	t.Helper()

	ctx := context.Background()
	if noPeerInfo {
		return ctx
	}

	var authInfo credentials.AuthInfo
	if !noAuthInfo {
		uid := permissions.CurrentUserUID()
		pid := os.Getpid()
		if pid > math.MaxInt32 {
			require.Fail(t, "Setup: pid is too large to be converted to int32: %d", pid)
		}
		//nolint:gosec // we checked for an integer overflow above.
		authInfo = permissions.NewTestPeerAuthInfo(uid, int32(pid))
	}
	p := peer.Peer{
		AuthInfo: authInfo,
	}
	return peer.NewContext(ctx, &p)
}
