package pam_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var errTest = errors.New("an error")
var privateKey *rsa.PrivateKey

func TestAvailableBrokers(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client authd.PAMClient

		wantRet   *authd.ABResponse
		wantError error
	}{
		"With_empty_options": {
			client:  NewDummyClient(nil),
			wantRet: &authd.ABResponse{},
		},
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithAvailableBrokers(nil, errTest)),
			wantError: errTest,
		},
		"With_defined_return_value": {
			client: NewDummyClient(nil, WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
				{
					Id:        "testBroker",
					Name:      "A test broker",
					BrandIcon: ptrValue("/usr/share/icons/broker-icon.png"),
				},
			}, nil)),
			wantRet: &authd.ABResponse{
				BrokersInfos: []*authd.ABResponse_BrokerInfo{
					{
						Id:        "testBroker",
						Name:      "A test broker",
						BrandIcon: ptrValue("/usr/share/icons/broker-icon.png"),
					},
				},
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ret, err := tc.client.AvailableBrokers(context.TODO(), nil)
			require.ErrorIs(t, err, tc.wantError)
			require.Equal(t, tc.wantRet, ret)
		})
	}
}

func TestGetPreviousBroker(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client authd.PAMClient
		args   *authd.GPBRequest

		wantRet   *authd.GPBResponse
		wantError error
	}{
		"With_empty_options": {
			client:  NewDummyClient(nil),
			wantRet: &authd.GPBResponse{},
		},
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithGetPreviousBrokerReturn("", errTest)),
			wantError: errTest,
		},
		"With_defined_return_value": {
			client: NewDummyClient(nil, WithGetPreviousBrokerReturn("my-previous-broker", nil)),
			wantRet: &authd.GPBResponse{
				PreviousBroker: "my-previous-broker",
			},
		},
		"With_defined_empty_return_value": {
			client:  NewDummyClient(nil, WithGetPreviousBrokerReturn("", nil)),
			wantRet: &authd.GPBResponse{},
		},
		"With_predefined_default_for_user_empty_return_value": {
			client: NewDummyClient(nil,
				WithPreviousBrokerForUser("user0", "broker0"),
				WithPreviousBrokerForUser("user1", "broker1"),
			),
			args:    &authd.GPBRequest{Username: "user1"},
			wantRet: &authd.GPBResponse{PreviousBroker: "broker1"},
		},

		// Error cases
		"Error_with_missing_user": {
			client:    NewDummyClient(nil, WithGetPreviousBrokerReturn("", nil)),
			args:      &authd.GPBRequest{},
			wantError: errors.New("no username provided"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ret, err := tc.client.GetPreviousBroker(context.TODO(), tc.args)
			require.Equal(t, tc.wantError, err)
			require.Equal(t, tc.wantRet, ret)
		})
	}
}

func TestSelectBroker(t *testing.T) {
	t.Parallel()

	pubASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	wantEncryptionKey := base64.StdEncoding.EncodeToString(pubASN1)

	testCases := map[string]struct {
		client            authd.PAMClient
		args              *authd.SBRequest
		reselectAgainUser string

		wantGeneratedSessionID bool
		wantRet                *authd.SBResponse
		wantError              error
	}{
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithSelectBrokerReturn(nil, errTest)),
			wantError: errTest,
		},
		"With_valid_args_and_generated_return_value": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(nil, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args:                   &authd.SBRequest{BrokerId: "test-broker"},
			wantGeneratedSessionID: true,
		},
		"With_valid_args_and_empty_return_value": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(&authd.SBResponse{}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args:                   &authd.SBRequest{BrokerId: "test-broker"},
			wantRet:                &authd.SBResponse{},
			wantGeneratedSessionID: true,
		},
		"With_valid_args_and_empty_return_value_with_ignored_ID_generation": {
			client: NewDummyClient(nil,
				WithIgnoreSessionIDGeneration(),
				WithSelectBrokerReturn(&authd.SBResponse{}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args:    &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{},
		},
		"With_valid_args_and_defined_return_value": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(&authd.SBResponse{
					SessionId:     "session-id",
					EncryptionKey: "super-secret-encryption-key",
				}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{
				SessionId:     "session-id",
				EncryptionKey: "super-secret-encryption-key",
			},
		},
		"With_private_key_and_valid_args_and_empty_return_value": {
			client: NewDummyClient(privateKey,
				WithSelectBrokerReturn(&authd.SBResponse{}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{
				EncryptionKey: wantEncryptionKey,
			},
			wantGeneratedSessionID: true,
		},
		"With_private_key_and_valid_args,_empty_return_value_ignoring_session_ID_generation": {
			client: NewDummyClient(privateKey,
				WithIgnoreSessionIDGeneration(),
				WithSelectBrokerReturn(&authd.SBResponse{}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{
				EncryptionKey: wantEncryptionKey,
			},
		},
		"With_private_key_and_valid_args_and_defined_return_value": {
			client: NewDummyClient(privateKey,
				WithSelectBrokerReturn(&authd.SBResponse{
					SessionId:     "session-id",
					EncryptionKey: "super-secret-encryption-key",
				}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "dummy-broker",
						Name: "A Dummy broker",
					},
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{
				SessionId:     "session-id",
				EncryptionKey: "super-secret-encryption-key",
			},
		},
		"With_private_key_and_valid_args_and_defined_return_value_without_encryption_key": {
			client: NewDummyClient(privateKey,
				WithSelectBrokerReturn(&authd.SBResponse{
					SessionId: "session-id",
				}, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "dummy-broker",
						Name: "A Dummy broker",
					},
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{BrokerId: "test-broker"},
			wantRet: &authd.SBResponse{
				SessionId:     "session-id",
				EncryptionKey: wantEncryptionKey,
			},
		},
		"Starting_a_session_for_same_user_is_fine": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(nil, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{
				BrokerId: "test-broker",
				Username: "an-user",
			},
			reselectAgainUser:      "an-user",
			wantGeneratedSessionID: true,
			wantRet:                &authd.SBResponse{},
		},
		"Starting_a_session_for_another_user_is_fine_when_ignoring_ID_checks": {
			client: NewDummyClient(nil,
				WithIgnoreSessionIDChecks(),
				WithIgnoreSessionIDGeneration(),
				WithSelectBrokerReturn(nil, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{
				BrokerId: "test-broker",
				Username: "an-user",
			},
			reselectAgainUser: "another-user",
			wantRet:           &authd.SBResponse{},
		},

		// Error cases
		"Error_with_nil_args_and_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"Error_with_empty_args_empty_options": {
			client:    NewDummyClient(nil),
			args:      &authd.SBRequest{},
			wantError: errors.New("no broker ID provided"),
		},
		"Error_on_unknown_broker_id": {
			client:    NewDummyClient(nil, WithSelectBrokerReturn(nil, nil)),
			args:      &authd.SBRequest{BrokerId: "some-broker"},
			wantError: fmt.Errorf(`broker "some-broker" not found`),
		},
		"Error_on_starting_a_session_again": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(nil, nil),
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
					{
						Id:   "test-broker",
						Name: "A test broker",
					},
				}, nil)),
			args: &authd.SBRequest{
				BrokerId: "test-broker",
				Username: "an-user",
			},
			reselectAgainUser:      "another-user",
			wantGeneratedSessionID: true,
		},
		"Error_on_broker_fetching_failed": {
			client: NewDummyClient(nil,
				WithSelectBrokerReturn(nil, nil),
				WithAvailableBrokers(nil, errTest)),
			args:      &authd.SBRequest{BrokerId: "test-broker"},
			wantError: errTest,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ret, err := tc.client.SelectBroker(context.TODO(), tc.args)
			require.Equal(t, tc.wantError, err)
			if tc.wantGeneratedSessionID {
				require.NotNil(t, ret)
				_, err := uuid.Parse(ret.SessionId)
				require.NoError(t, err)
				if tc.wantRet != nil {
					tc.wantRet.SessionId = ret.SessionId
				} else {
					tc.wantRet = ret
				}
			}
			require.Equal(t, tc.wantRet, ret)
			if err != nil {
				require.Nil(t, ret)
				return
			}

			dc, ok := tc.client.(*DummyClient)
			require.True(t, ok, "Provided client is not a Dummy client")
			if ret != nil {
				require.Equal(t, ret.SessionId, dc.CurrentSessionID())
			}

			if tc.args == nil {
				require.Empty(t, dc.SelectedBrokerID())
				require.Empty(t, dc.SelectedLang())
				require.Empty(t, dc.SelectedUsername())
				return
			}
			require.Equal(t, tc.args.GetBrokerId(), dc.SelectedBrokerID())
			require.Equal(t, tc.args.GetLang(), dc.SelectedLang())
			require.Equal(t, tc.args.GetUsername(), dc.SelectedUsername())

			if tc.reselectAgainUser != "" {
				ret, err := tc.client.SelectBroker(context.TODO(), tc.args)
				require.Nil(t, err)
				require.Equal(t, tc.wantRet, ret)
			}
		})
	}
}

func startBrokerSession(t *testing.T, client authd.PAMClient, brokerIdx int) string {
	t.Helper()

	brokers, err := client.AvailableBrokers(context.TODO(), nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(brokers.BrokersInfos), brokerIdx)
	ret, err := client.SelectBroker(context.TODO(), &authd.SBRequest{
		BrokerId: brokers.BrokersInfos[brokerIdx].Id,
	})
	require.NoError(t, err)
	require.NotNil(t, ret)
	return ret.SessionId
}

func TestGetAuthenticationModes(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client              authd.PAMClient
		args                *authd.GAMRequest
		skipBrokerSelection bool

		wantRet   *authd.GAMResponse
		wantError error
	}{
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithGetAuthenticationModesReturn(nil, errTest)),
			wantError: errTest,
		},
		"With_empty_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithGetAuthenticationModesReturn(nil, nil),
			),
			args:    &authd.GAMRequest{SessionId: "started-session-id"},
			wantRet: &authd.GAMResponse{AuthenticationModes: []*authd.GAMResponse_AuthenticationMode{}},
		},
		"With_all_modes_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithGetAuthenticationModesReturn([]*authd.GAMResponse_AuthenticationMode{
					{
						Id:    "foo",
						Label: "Bar",
					},
					{
						Id:    "password",
						Label: "Insert your password",
					},
				}, nil),
			),
			args: &authd.GAMRequest{SessionId: "started-session-id"},
			wantRet: &authd.GAMResponse{
				AuthenticationModes: []*authd.GAMResponse_AuthenticationMode{
					{
						Id:    "foo",
						Label: "Bar",
					},
					{
						Id:    "password",
						Label: "Insert your password",
					},
				},
			},
		},
		"With_modes_returned_from_values": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithUILayout("foobar", "Baz", &authd.UILayout{}),
				WithUILayout("password", "Insert your password", FormUILayout()),
			),
			args: &authd.GAMRequest{SessionId: "started-session-id"},
			wantRet: &authd.GAMResponse{
				AuthenticationModes: []*authd.GAMResponse_AuthenticationMode{
					{
						Id:    "foobar",
						Label: "Baz",
					},
					{
						Id:    "password",
						Label: "Insert your password",
					},
				},
			},
		},
		"With_no_session_ID_arg_when_enabled": {
			client:  NewDummyClient(nil, WithIgnoreSessionIDChecks()),
			args:    &authd.GAMRequest{},
			wantRet: &authd.GAMResponse{AuthenticationModes: []*authd.GAMResponse_AuthenticationMode{}},
		},

		// Error cases
		"Error_with_nil_args_and_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"Error_with_no_session_ID_arg": {
			client:    NewDummyClient(nil),
			args:      &authd.GAMRequest{},
			wantError: errors.New("no session ID provided"),
		},
		"Error_with_not-matching_session_ID": {
			client:              NewDummyClient(nil),
			args:                &authd.GAMRequest{SessionId: "session-id"},
			skipBrokerSelection: true,
			wantError:           errors.New(`impossible to get authentication mode, session ID "session-id" not found`),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.args != nil && tc.args.SessionId != "" && !tc.skipBrokerSelection {
				sessionID := startBrokerSession(t, tc.client, 0)
				require.Equal(t, tc.args.SessionId, sessionID)
			}

			ret, err := tc.client.GetAuthenticationModes(context.TODO(), tc.args)
			require.Equal(t, tc.wantError, err)
			if err != nil {
				require.Nil(t, ret)
				return
			}

			require.Equal(t, tc.wantRet, ret)
		})
	}
}

func TestSelectAuthenticationModes(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client              authd.PAMClient
		args                *authd.SAMRequest
		skipBrokerSelection bool

		wantRet   *authd.SAMResponse
		wantError error
	}{
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithSelectAuthenticationModeReturn(nil, errTest)),
			wantError: errTest,
		},
		"With_empty_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithSelectAuthenticationModeReturn(&authd.UILayout{}, nil),
			),
			args:    &authd.SAMRequest{SessionId: "started-session-id"},
			wantRet: &authd.SAMResponse{UiLayoutInfo: &authd.UILayout{}},
		},
		"With_all_modes_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithUILayout("password", "Write the password", FormUILayout()),
				WithUILayout("pin", "Write the PIN number", FormUILayout()),
				WithUILayout(layouts.QrCode, "Scan the QR code", QrCodeUILayout()),
				WithUILayout("new-pass", "Update your password", NewPasswordUILayout()),
			),
			args: &authd.SAMRequest{
				SessionId:            "started-session-id",
				AuthenticationModeId: layouts.QrCode,
			},
			wantRet: &authd.SAMResponse{UiLayoutInfo: QrCodeUILayout()},
		},

		// Error cases
		"Error_with_nil_args_and_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"Error_with_no_session_ID_arg": {
			client:    NewDummyClient(nil),
			args:      &authd.SAMRequest{},
			wantError: errors.New("no session ID provided"),
		},
		"Error_with_not-matching_session_ID": {
			client:              NewDummyClient(nil),
			args:                &authd.SAMRequest{SessionId: "session-id"},
			skipBrokerSelection: true,
			wantError:           errors.New(`impossible to select authentication mode, session ID "session-id" not found`),
		},
		"Error_with_no_authentication_mode_ID": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithSelectAuthenticationModeReturn(nil, nil),
			),
			args:      &authd.SAMRequest{SessionId: "started-session-id"},
			wantError: errors.New("no authentication mode ID provided"),
		},
		"Error_unknown_authentication_mode_ID": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithSelectAuthenticationModeReturn(nil, nil),
			),
			args: &authd.SAMRequest{
				SessionId:            "started-session-id",
				AuthenticationModeId: "auth-mode-id",
			},
			wantError: errors.New(`authentication mode "auth-mode-id" not found`),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.args != nil && tc.args.SessionId != "" && !tc.skipBrokerSelection {
				sessionID := startBrokerSession(t, tc.client, 0)
				require.Equal(t, tc.args.SessionId, sessionID)
			}

			ret, err := tc.client.SelectAuthenticationMode(context.TODO(), tc.args)
			require.Equal(t, tc.wantError, err)
			require.Equal(t, tc.wantRet, ret)
		})
	}
}

func TestIsAuthenticated(t *testing.T) {
	t.Parallel()

	cancelledContext, cancel := context.WithCancel(context.TODO())
	cancel()

	testCases := map[string]struct {
		client              authd.PAMClient
		args                *authd.IARequest
		context             context.Context
		skipBrokerSelection bool

		wantRet   *authd.IAResponse
		wantError error
	}{
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithIsAuthenticatedReturn(nil, errTest)),
			wantError: errTest,
		},
		"With_empty_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedReturn(&authd.IAResponse{}, nil),
			),
			args:    &authd.IARequest{SessionId: "started-session-id"},
			wantRet: &authd.IAResponse{},
		},
		"With_retry_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Retry,
					Msg:    "Try again",
				}, nil),
			),
			args: &authd.IARequest{SessionId: "started-session-id"},
			wantRet: &authd.IAResponse{
				Access: auth.Retry,
				Msg:    "Try again",
			},
		},
		"Invalid_secret": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: encryptAndEncodeSecret(t, &privateKey.PublicKey, "invalid-password"),
					},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Denied,
			},
		},
		"Invalid_secret_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
				WithIsAuthenticatedMessage("You're out!"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: encryptAndEncodeSecret(t, &privateKey.PublicKey, "invalid-password"),
					},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Denied,
				Msg:    `{"message": "You're out!"}`,
			},
		},
		"Retry_challenge_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
				WithIsAuthenticatedMaxRetries(1),
				WithIsAuthenticatedMessage("try again!"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: encryptAndEncodeSecret(t, &privateKey.PublicKey, "invalid-password"),
					},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Retry,
				Msg:    `{"message": "try again!"}`,
			},
		},
		"Valid_secret": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: encryptAndEncodeSecret(t, &privateKey.PublicKey, "super-secret-password"),
					},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Granted,
			},
		},
		"Valid_secret_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
				WithIsAuthenticatedMessage("try again!"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: encryptAndEncodeSecret(t, &privateKey.PublicKey, "super-secret-password"),
					},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Granted,
				Msg:    `{"message": "try again!"}`,
			},
		},
		"Wait_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithUILayout("password", "Insert your password or wait", FormUILayout(WithWait(true))),
				WithIsAuthenticatedWantWait(time.Microsecond*5),
				WithIsAuthenticatedMessage("Wait done!"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Granted,
				Msg:    `{"message": "Wait done!"}`,
			},
		},
		"Wait_cancel": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithUILayout("password", "Insert your password or wait", FormUILayout(WithWait(true))),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantWait(time.Second*5),
			),
			context: cancelledContext,
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Cancelled,
			},
		},
		"Wait_cancel_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithUILayout("password", "Insert your password or wait", FormUILayout(WithWait(true))),
				WithIsAuthenticatedWantWait(time.Second*5),
				WithIsAuthenticatedMessage("oh no!"),
			),
			context: cancelledContext,
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Wait{Wait: layouts.True},
				},
			},
			wantRet: &authd.IAResponse{
				Access: auth.Cancelled,
				Msg:    `{"message": "Cancelled: oh no!"}`,
			},
		},
		"Skip_with_message": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSkip(),
				WithIsAuthenticatedMessage("Skip done!"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Skip{Skip: layouts.True},
				},
			},
			wantRet: &authd.IAResponse{
				Msg: `{"message": "Skip done!"}`,
			},
		},

		// Error cases
		"Error_with_nil_args_and_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"Error_with_no_session_ID_arg": {
			client:    NewDummyClient(nil),
			args:      &authd.IARequest{},
			wantError: errors.New("no session ID provided"),
		},
		"Error_with_not-matching_session_ID": {
			client:              NewDummyClient(nil),
			args:                &authd.IARequest{SessionId: "session-id"},
			skipBrokerSelection: true,
			wantError:           errors.New(`impossible to authenticate, session ID "session-id" not found`),
		},
		"Error_with_no_authentication_data": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
			),
			args:      &authd.IARequest{SessionId: "started-session-id"},
			wantError: errors.New("no authentication data provided"),
		},
		"Error_with_invalid_authentication_data": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("super-secret-password"),
			),
			args: &authd.IARequest{
				SessionId:          "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{},
			},
			wantError: errors.New("no authentication data provided"),
		},
		"Error_missing_wanted_secret": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{},
				},
			},
			wantError: errors.New("no wanted secret provided"),
		},
		"Error_missing_wanted_wait": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithUILayout("password", "Insert your password or wait", FormUILayout(WithWait(true))),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Wait{},
				},
			},
			wantError: errors.New("no wanted wait provided"),
		},
		"Error_missing_wanted_skip": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Skip{},
				},
			},
			wantError: errors.New("no wanted skip requested"),
		},
		"Error_empty_secret": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("secret"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{},
				},
			},
			wantError: errors.New("no secret provided"),
		},
		"Error_decoding_secret": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("secret"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: "Invalid base64",
					},
				},
			},
			wantError: base64.CorruptInputError(7),
		},
		"Error_decrypting_secret_per_missing_private_key": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("secret"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: base64.StdEncoding.EncodeToString([]byte("Invalid encrypted key")),
					},
				},
			},
			wantError: errors.New("no private key defined"),
		},
		"Error_decrypting_invalid_secret": {
			client: NewDummyClient(privateKey,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:   "test-broker",
					Name: "A test broker",
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithIsAuthenticatedWantSecret("secret"),
			),
			args: &authd.IARequest{
				SessionId: "started-session-id",
				AuthenticationData: &authd.IARequest_AuthenticationData{
					Item: &authd.IARequest_AuthenticationData_Secret{
						Secret: base64.StdEncoding.EncodeToString([]byte("Invalid encrypted key")),
					},
				},
			},
			wantError: rsa.ErrDecryption,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.args != nil && tc.args.SessionId != "" && !tc.skipBrokerSelection {
				sessionID := startBrokerSession(t, tc.client, 0)
				require.Equal(t, tc.args.SessionId, sessionID)
			}

			context := context.TODO()
			if tc.context != nil {
				context = tc.context
			}
			ret, err := tc.client.IsAuthenticated(context, tc.args)
			require.Equal(t, tc.wantError, err)
			require.Equal(t, tc.wantRet, ret)
		})
	}
}

func encryptAndEncodeSecret(t *testing.T, pubKey *rsa.PublicKey, secret string) string {
	t.Helper()

	ciphertext, err := rsa.EncryptOAEP(sha512.New(), rand.Reader, pubKey, []byte(secret), nil)
	require.NoError(t, err)

	// encrypt it to base64 and replace the secret with it
	base64Encoded := base64.StdEncoding.EncodeToString(ciphertext)
	return base64Encoded
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client           authd.PAMClient
		args             *authd.ESRequest
		selectedBrokerID string

		wantError error
	}{
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithEndSessionReturn(errTest)),
			wantError: errTest,
		},
		"With_valid_return_value": {
			client: NewDummyClient(nil,
				WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{{
					Id:        "test-broker",
					Name:      "A test broker",
					BrandIcon: ptrValue("/usr/share/icons/broker-icon.png"),
				}}, nil),
				WithSelectBrokerReturn(&authd.SBResponse{SessionId: "started-session-id"}, nil),
				WithEndSessionReturn(nil),
			),
			args:             &authd.ESRequest{SessionId: "started-session-id"},
			selectedBrokerID: "test-broker",
		},

		// Error cases
		"Error_with_nil_args_and_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"Error_with_empty_args_empty_options": {
			client:    NewDummyClient(nil),
			args:      &authd.ESRequest{},
			wantError: errors.New("no session ID provided"),
		},
		"Error_with_not-matching_session_ID": {
			client:    NewDummyClient(nil),
			args:      &authd.ESRequest{SessionId: "a-session-id"},
			wantError: errors.New(`impossible to end session "a-session-id", not found`),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dc, ok := tc.client.(*DummyClient)
			require.True(t, ok, "Provided client is not a Dummy client")

			if tc.args != nil && tc.args.SessionId != "" && tc.selectedBrokerID != "" {
				ret, err := tc.client.SelectBroker(context.TODO(), &authd.SBRequest{
					BrokerId: tc.selectedBrokerID,
				})
				require.NoError(t, err)
				require.NotNil(t, ret)
				require.Equal(t, tc.args.SessionId, ret.SessionId)
				require.Equal(t, tc.args.SessionId, dc.CurrentSessionID())
			}

			ret, err := tc.client.EndSession(context.TODO(), tc.args)
			require.Equal(t, tc.wantError, err)
			if err != nil {
				require.Nil(t, ret)
				return
			}

			require.Equal(t, &authd.Empty{}, ret)
			require.Empty(t, dc.CurrentSessionID())
			require.Empty(t, dc.SelectedUsername())
		})
	}
}

func TestSetDefaultBrokerForUser(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		client authd.PAMClient
		args   *authd.SDBFURequest

		wantError error
	}{
		"With_empty_options": {
			client:    NewDummyClient(nil),
			wantError: errors.New("no input values provided"),
		},
		"With_Error_return_value": {
			client:    NewDummyClient(nil, WithSetDefaultBrokerReturn(errTest)),
			wantError: errTest,
		},
		"With_valid_arguments": {
			client: NewDummyClient(nil, WithSetDefaultBrokerReturn(nil)),
			args: &authd.SDBFURequest{
				BrokerId: "broker-id",
				Username: "username",
			},
		},

		// Error cases
		"Error_if_no_user_name_is_provided": {
			client:    NewDummyClient(nil),
			args:      &authd.SDBFURequest{BrokerId: "broker-id"},
			wantError: errors.New("no valid username provided"),
		},
		"Error_if_no_broker_ID_is_provided": {
			client:    NewDummyClient(nil),
			args:      &authd.SDBFURequest{Username: "username"},
			wantError: errors.New("no valid broker ID provided"),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ret, err := tc.client.SetDefaultBrokerForUser(context.TODO(), tc.args)
			require.Equal(t, err, tc.wantError)
			if err != nil {
				require.Nil(t, ret)
				return
			}

			require.Equal(t, &authd.Empty{}, ret)

			if tc.args == nil {
				return
			}
			retBroker, err := tc.client.GetPreviousBroker(context.TODO(),
				&authd.GPBRequest{Username: tc.args.Username})
			require.NoError(t, err)
			require.Equal(t, tc.args.BrokerId, retBroker.PreviousBroker)
		})
	}
}

func TestMain(m *testing.M) {
	var err error
	privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("could not create an valid rsa key: %v", err))
	}

	m.Run()
}
