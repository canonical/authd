package adapter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/testutils"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/gdm_test"
	"github.com/canonical/authd/pam/internal/pam_test"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/msteinert/pam/v2"
	"github.com/stretchr/testify/require"
)

var gdmTestPrivateKey *rsa.PrivateKey

const gdmTestIgnoredMessage string = "<ignored>"

func TestGdmModel(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	// This is not technically an error, as it means that during the tests
	// we've stopped the program with a Quit request.
	// However we do return a PAM error in such case because that's what we're
	// going to return to the PAM stack in case authentication process has not
	// been completed fully.
	gdmTestEarlyStopExitStatus := errNoExitStatus

	gdmTestIgnoreStage := proto.Stage(-1)

	firstBrokerInfo := &authd.ABResponse_BrokerInfo{
		Id:        "testBroker",
		Name:      "The best broker!",
		BrandIcon: nil,
	}
	secondBrokerInfo := &authd.ABResponse_BrokerInfo{
		Id:        "secondaryBroker",
		Name:      "A broker that works too!",
		BrandIcon: nil,
	}

	passwordUILayoutID := "Password"
	singleBrokerClientOptions := []pam_test.DummyClientOptions{
		// FIXME: Ideally we should use proper ID checks, but this can currently lead to
		// races because the way our model is implemented and events can't be stopped and may
		// arrive con delay.
		pam_test.WithIgnoreSessionIDChecks(),
		pam_test.WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
			firstBrokerInfo,
		}, nil),
		pam_test.WithUILayout(passwordUILayoutID, "Password authentication", pam_test.FormUILayout()),
	}
	newPasswordUILayoutID := layouts.NewPassword
	singleBrokerNewPasswordClientOptions := []pam_test.DummyClientOptions{
		pam_test.WithIgnoreSessionIDChecks(),
		pam_test.WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
			firstBrokerInfo,
		}, nil),
		pam_test.WithUILayout(newPasswordUILayoutID, "New Password form", pam_test.NewPasswordUILayout()),
	}
	multiBrokerClientOptions := append(slices.Clone(singleBrokerClientOptions),
		pam_test.WithAvailableBrokers([]*authd.ABResponse_BrokerInfo{
			firstBrokerInfo, secondBrokerInfo,
		}, nil),
	)

	testCases := map[string]struct {
		client           authd.PAMClient
		clientOptions    []pam_test.DummyClientOptions
		sessionMode      authd.SessionMode
		supportedLayouts []*authd.UILayout
		messages         []tea.Msg
		commands         []tea.Cmd
		gdmEvents        []*gdm.EventData
		pamUser          string
		protoVersion     uint32
		convError        map[string]error
		timeout          time.Duration

		wantExitStatus     PamReturnStatus
		wantGdmRequests    []gdm.RequestType
		wantGdmEvents      []gdm.EventType
		wantGdmAuthRes     []*authd.IAResponse
		wantNoGdmRequests  []gdm.RequestType
		wantNoGdmEvents    []gdm.EventType
		wantNoBrokers      bool
		wantSelectedBroker string
		wantStage          proto.Stage
		wantUsername       string
		wantMessages       []tea.Msg
	}{
		"User_selection_stage": {
			wantGdmRequests: []gdm.RequestType{gdm.RequestType_uiLayoutCapabilities},
			wantStage:       proto.Stage_userSelection,
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
			},
			wantNoGdmRequests: []gdm.RequestType{
				gdm.RequestType_changeStage, // -> broker Selection
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Broker_selection_stage_caused_by_PAM_user_selection": {
			pamUser: "pam-preset-user",
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_brokerSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Broker_selection_stage_caused_by_client_side_user_selection": {
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user"),
			},
			wantUsername: "gdm-selected-user",
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_brokerSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Challenge_stage_caused_by_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil)),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{stage: proto.Stage_challenge},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Challenge_stage_caused_by_client_side_broker_and_authMode_selection": {
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("GDM-SELECTED-USER-AND-BROKER"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{stage: proto.Stage_challenge},
			},
			wantUsername:       "gdm-selected-user-and-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Authenticated_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password")),
			pamUser: "pam-PRESET-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Granted}},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_preset_PAM_user_using_legacy_challenge_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password")),
			pamUser: "pam-PRESET-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Challenge{
							Challenge: "gdm-good-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Granted}},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_preset_PAM_user_updated_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password")),
			pamUser: "GDM@PAM-CASE-INDEPENDENT-PRESET-USER",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_userSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_userSelection,
							events: []*gdm.EventData{
								gdm_test.SelectUserEvent("gdm@pam-case-independent-preset-user"),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestWaitForStage{
									stage: proto.Stage_challenge,
									commands: []tea.Cmd{
										sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
											Secret: "gdm-good-password",
										}}),
									},
								}),
							},
						}),
					},
				},
			},
			wantMessages: []tea.Msg{
				userSelected{"GDM@PAM-CASE-INDEPENDENT-PRESET-USER"},
				userSelected{"gdm@pam-case-independent-preset-user"},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected, // First selection, done from PAM.
				gdm.EventType_userSelected, // Second injected (and accepted) selection.
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Granted}},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_message_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Granted,
					Msg:    `{"message": "Hi GDM, it's a pleasure to get you in!"}`,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent,
				gdm.EventType_startAuthentication,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Granted,
				Msg:    "Hi GDM, it's a pleasure to get you in!",
			}},
			wantExitStatus: PamSuccess{
				BrokerID: firstBrokerInfo.Id,
				msg:      "Hi GDM, it's a pleasure to get you in!",
			},
		},
		"New_password_changed_after_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerNewPasswordClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Granted,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			supportedLayouts:   []*authd.UILayout{pam_test.NewPasswordUILayout()},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Granted,
			}},
			wantExitStatus: PamSuccess{
				BrokerID: firstBrokerInfo.Id,
			},
		},
		"New_password_changed_with_message_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerNewPasswordClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Granted,
					Msg:    `{"message": "Hi GDM, it's a pleasure to change your password!"}`,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			supportedLayouts:   []*authd.UILayout{pam_test.NewPasswordUILayout()},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Granted,
				Msg:    "Hi GDM, it's a pleasure to change your password!",
			}},
			wantExitStatus: PamSuccess{
				BrokerID: firstBrokerInfo.Id,
				msg:      "Hi GDM, it's a pleasure to change your password!",
			},
		},
		"New_password_cannot_change_because_not_respecting_rules_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerNewPasswordClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Granted,
					Msg:    `{"message": "Hi GDM, it's a pleasure to change your password!"}`,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "newpass",
						}}),
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "password",
						}}),
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			supportedLayouts:   []*authd.UILayout{pam_test.NewPasswordUILayout()},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // retry
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // retry
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // granted
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{
					Access: auth.Retry,
					Msg:    "The password is shorter than 8 characters",
				},
				{
					Access: auth.Retry,
					Msg:    "The password fails the dictionary check - it is based on a dictionary word",
				},
				{
					Access: auth.Granted,
					Msg:    "Hi GDM, it's a pleasure to change your password!",
				},
			},
			wantExitStatus: PamSuccess{
				BrokerID: firstBrokerInfo.Id,
				msg:      "Hi GDM, it's a pleasure to change your password!",
			},
		},
		"Changing_password_fails_when_same_as_old_password_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			timeout:     30 * time.Second,
			sessionMode: authd.SessionMode_CHANGE_PASSWORD,
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithUILayout(newPasswordUILayoutID, "New Password", pam_test.NewPasswordUILayout()),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Next,
					Msg:    `{"message": "Hi GDM, it's a pleasure to let you change your password!"}`,
				}, nil),
			),
			pamUser: "pam-preset-user-with-daemon-selected-broker-and-auth-mode",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_authModeSelection,
					events: []*gdm.EventData{
						gdm_test.AuthModeSelectedEvent(passwordUILayoutID),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Secret{
								Secret: "gdm-repeated-password",
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestWaitForStage{
									stage: proto.Stage_authModeSelection,
								}),
								sendEvent(gdmTestWaitForStage{
									stage: proto.Stage_challenge,
									events: []*gdm.EventData{
										gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
									},
									commands: []tea.Cmd{
										sendEvent(gdmTestWaitForStage{
											stage: proto.Stage_authModeSelection,
											events: []*gdm.EventData{
												gdm_test.AuthModeSelectedEvent(newPasswordUILayoutID),
											},
											commands: []tea.Cmd{
												sendEvent(gdmTestWaitForStage{
													stage: proto.Stage_challenge,
													commands: []tea.Cmd{
														sendEvent(gdmTestSendAuthDataWhenReady{
															&authd.IARequest_AuthenticationData_Secret{
																Secret: "gdm-repeated-password",
															},
														}),
													},
												}),
											},
										}),
									},
								}),
							},
						}),
					},
				},
			},
			supportedLayouts: []*authd.UILayout{
				pam_test.FormUILayout(pam_test.WithWait(true)),
				pam_test.NewPasswordUILayout(),
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent, // next
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent, // retry
				gdm.EventType_startAuthentication,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{
					Access: auth.Next,
					Msg:    "Hi GDM, it's a pleasure to let you change your password!",
				},
				{
					Access: auth.Retry,
					Msg:    "The password is the same as the old one",
				},
			},
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Authentication_is_ignored_if_not_requested_by_model_first": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password")),
			gdmEvents: []*gdm.EventData{
				gdm_test.IsAuthenticatedEvent(&authd.IARequest_AuthenticationData_Secret{
					Secret: "gdm-good-password",
				}),
			},
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantNoGdmRequests: []gdm.RequestType{
				gdm.RequestType_changeStage,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_userSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Cancelled_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Cancelled,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
					Secret: "gdm-any-password",
				}},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Cancelled}},
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Explicitly_cancelled_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.IsAuthenticatedCancelledEvent(),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
			},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Explicitly_cancelled_for_a_waiting_auth_mode_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantWait(time.Millisecond*1500),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Wait{
								Wait: layouts.True,
							},
							events: []*gdm.EventData{
								gdm_test.IsAuthenticatedCancelledEvent(),
							},
						}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> form with wait
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Cancelled}},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Implicitly_cancelled_for_a_waiting_auth_mode_with_preset_PAM_user_and_server_side_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantWait(time.Millisecond*1500),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Wait{
								Wait: layouts.True,
							},
							events: []*gdm.EventData{
								gdm_test.ChangeStageEvent(proto.Stage_brokerSelection),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestWaitForStage{stage: proto.Stage_brokerSelection}),
							},
						}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> form with wait
				gdm.RequestType_changeStage, // -> broker selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Cancelled}},
			wantStage:      proto.Stage_brokerSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Authenticated_with_preset_PAM_user_and_server_side_broker_and_authMode_selection_and_after_various_retries": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
				pam_test.WithIsAuthenticatedMaxRetries(1),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Secret{
								Secret: "gdm-bad-password",
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{
									&authd.IARequest_AuthenticationData_Secret{
										Secret: "gdm-good-password",
									},
								}),
							},
						}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // retry
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // granted
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Retry},
				{Access: auth.Granted},
			},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_after_client_side_user_and_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(multiBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(secondBrokerInfo.Id),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-good-password",
						}}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: secondBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Granted}},
			wantExitStatus: PamSuccess{BrokerID: secondBrokerInfo.Id},
		},
		"Authenticated_after_client_side_user_and_broker_and_authMode_selection_and_after_various_retries": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
				pam_test.WithIsAuthenticatedMaxRetries(1),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Secret{
								Secret: "gdm-bad-password",
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{
									&authd.IARequest_AuthenticationData_Secret{
										Secret: "gdm-good-password",
									},
								}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Retry},
				{Access: auth.Granted},
			},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Cancelled_auth_after_client_side_user_and_broker_and_authMode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Cancelled,
				}, nil),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
					Secret: "gdm-some-password",
				}},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
			},
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Cancelled}},
			wantStage:      proto.Stage_challenge,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"AuthMode_selection_stage_from_client_after_server_side_broker_and_auth_mode_selection_if_there_is_only_one_auth_mode": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{stage: proto.Stage_authModeSelection}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_authModeSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"AuthMode_selection_stage_from_client_after_server_side_broker_and_auth_mode_selection_with_multiple_auth_modes": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithUILayout("pincode", "Pin Code", pam_test.FormUILayout()),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{stage: proto.Stage_authModeSelection}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_authModeSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"AuthMode_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection_if_there_is_only_one_auth_mode": {
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{stage: proto.Stage_authModeSelection}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_authModeSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"Authenticated_after_auth_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection_if_there_is_only_one_auth_mode": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.AuthModeSelectedEvent(passwordUILayoutID),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
									Secret: "gdm-good-password",
								}}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Granted},
			},
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_after_auth_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection_with_multiple_auth_modes": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout("pincode", "Write the pin Code", pam_test.FormUILayout()),
				pam_test.WithIsAuthenticatedWantSecret("1234"),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.AuthModeSelectedEvent("pincode"),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
									Secret: "1234",
								}}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent,
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Granted},
			},
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_qrcode_after_auth_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection": {
			supportedLayouts: []*authd.UILayout{
				pam_test.FormUILayout(pam_test.WithWait(true)),
				pam_test.QrCodeUILayout(),
			},
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout(layouts.QrCode, "Hello QR!", pam_test.QrCodeUILayout()),
				pam_test.WithIsAuthenticatedWantWait(time.Millisecond*1500),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.AuthModeSelectedEvent(layouts.QrCode),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Wait{
									Wait: layouts.True,
								}}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> qrcode
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // granted
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Granted},
			},
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_qrcode_regenerated_after_auth_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection": {
			timeout: 30 * time.Second,
			supportedLayouts: []*authd.UILayout{
				pam_test.FormUILayout(pam_test.WithWait(true)),
				pam_test.QrCodeUILayout(),
			},
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout(layouts.QrCode, "Hello QR!", pam_test.QrCodeUILayout()),
				pam_test.WithIsAuthenticatedWantWait(time.Millisecond*1500),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.AuthModeSelectedEvent(layouts.QrCode),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReadyFull{
									authData: &authd.IARequest_AuthenticationData_Wait{
										Wait: layouts.True,
									},
									commands: []tea.Cmd{
										sendEvent(gdmTestAddPollResultEvent{
											gdm_test.ReselectAuthMode(),
										}),
										sendEvent(gdmTestSendAuthDataWhenReady{
											&authd.IARequest_AuthenticationData_Wait{
												Wait: layouts.True,
											},
										}),
									},
								}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> qrcode
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_authEvent, // cancelled
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authEvent, // granted
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Cancelled},
				{Access: auth.Granted},
			},
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Authenticated_with_qrcode_regenerated_after_wait_started_at_auth_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection": {
			supportedLayouts: []*authd.UILayout{
				pam_test.FormUILayout(pam_test.WithWait(true)),
				pam_test.QrCodeUILayout(),
			},
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout(layouts.QrCode, "Hello QR!", pam_test.QrCodeUILayout()),
				// It's long because we want to cancel this!
				pam_test.WithIsAuthenticatedWantWait(time.Millisecond*1500),
			),
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.AuthModeSelectedEvent(layouts.QrCode),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReadyFull{
									authData: &authd.IARequest_AuthenticationData_Wait{
										Wait: layouts.True,
									},
									commands: []tea.Cmd{
										sendEvent(gdmTestAddPollResultEvent{
											gdm_test.ReselectAuthMode(),
										}),
										sendEvent(gdmTestSendAuthDataWhenReady{
											&authd.IARequest_AuthenticationData_Wait{
												Wait: layouts.True,
											},
										}),
									},
								}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> qrcode
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
				startAuthentication{},
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
				gdm.EventType_startAuthentication,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_authEvent, // cancelled
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_startAuthentication,
				gdm.EventType_authModeSelected,
				gdm.EventType_authEvent, // granted
			},
			wantStage: proto.Stage_challenge,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Cancelled},
				{Access: auth.Granted},
			},
			wantExitStatus: PamSuccess{BrokerID: firstBrokerInfo.Id},
		},
		"Broker_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection_if_there_is_only_one_auth_mode": {
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.ChangeStageEvent(proto.Stage_brokerSelection),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestWaitForStage{stage: proto.Stage_brokerSelection}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> broker Selection
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_brokerSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},
		"User_selection_stage_from_client_after_client_side_broker_and_auth_mode_selection_if_there_is_only_one_auth_mode": {
			gdmEvents: []*gdm.EventData{
				gdm_test.SelectUserEvent("gdm-selected-user-broker-and-auth-mode"),
			},
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_authModeSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_authModeSelection,
							events: []*gdm.EventData{
								gdm_test.ChangeStageEvent(proto.Stage_brokerSelection),
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestWaitForStage{
									stage: proto.Stage_brokerSelection,
									events: []*gdm.EventData{
										gdm_test.ChangeStageEvent(proto.Stage_userSelection),
									},
									commands: []tea.Cmd{
										sendEvent(gdmTestWaitForStage{stage: proto.Stage_userSelection}),
									},
								}),
							},
						}),
					},
				},
			},
			wantUsername:       "gdm-selected-user-broker-and-auth-mode",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> user Selection
			},
			wantMessages: []tea.Msg{
				startAuthentication{},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModeSelected,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_authEvent,
			},
			wantStage:      proto.Stage_userSelection,
			wantExitStatus: gdmTestEarlyStopExitStatus,
		},

		// Error cases
		"Error_on_no_UI_layouts": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout(passwordUILayoutID, "", &authd.UILayout{}),
			),
			supportedLayouts: []*authd.UILayout{},
			wantGdmRequests:  []gdm.RequestType{gdm.RequestType_uiLayoutCapabilities},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
				gdm.EventType_userSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrCredUnavail,
				msg:    "UI does not support any layouts",
			},
		},
		"Error_on_brokers_fetching_error": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithAvailableBrokers(nil, errors.New("brokers loading failed")),
			),
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
				gdm.EventType_userSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "could not get current available brokers: brokers loading failed",
			},
			wantNoBrokers: true,
		},
		"Error_on_forced_quit": {
			messages:       []tea.Msg{tea.Quit()},
			wantExitStatus: gdmTestEarlyStopExitStatus,
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
			},
		},
		"Error_on_invalid_poll_data_response_for_missing_type": {
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			gdmEvents: []*gdm.EventData{
				{
					Type: gdm.EventType_userSelected,
				},
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "Sending GDM poll failed: Conversation error: poll response data member 0 invalid: missing event data",
			},
		},
		"Error_on_invalid_poll_data_response_for_missing_data": {
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			gdmEvents: []*gdm.EventData{{}},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "Sending GDM poll failed: Conversation error: poll response data member 0 invalid: missing event type",
			},
		},
		"Error_on_no_brokers": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithAvailableBrokers(nil, nil),
			),
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
				gdm.EventType_userSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrAuthinfoUnavail,
				msg:    "No brokers available",
			},
		},
		"Error_on_invalid_broker_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithSelectBrokerReturn(nil, errors.New("error during broker selection")),
			),
			pamUser:            "pam-preset-user-and-daemon-selected-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "error during broker selection",
			},
		},
		"Error_during_broker_selection_if_session_ID_is_empty": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIgnoreSessionIDGeneration(),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithSelectBrokerReturn(&authd.SBResponse{}, nil),
			),
			pamUser:            "pam-preset-user-and-daemon-selected-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "no session ID returned by broker",
			},
		},
		"Error_during_broker_selection_if_encryption_key_is_empty": {
			client: pam_test.NewDummyClient(nil, append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithSelectBrokerReturn(&authd.SBResponse{SessionId: "session-id"}, nil),
			)...),
			pamUser:            "pam-preset-user-and-daemon-selected-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "no encryption key returned by broker",
			},
		},
		"Error_during_broker_selection_if_encryption_key_is_not_valid_base64": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithSelectBrokerReturn(&authd.SBResponse{
					SessionId:     "session-id",
					EncryptionKey: "no encryption key returned by broker",
				}, nil),
			),
			pamUser:            "pam-preset-user-and-daemon-selected-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "encryption key sent by broker is not a valid base64 encoded string: illegal base64 data at input byte 2",
			},
		},
		"Error_during_broker_selection_if_encryption_key_is_not_valid_key": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithSelectBrokerReturn(&authd.SBResponse{
					SessionId: "session-id",
					EncryptionKey: base64.StdEncoding.EncodeToString(
						[]byte("not a valid encryption key!")),
				}, nil),
			),
			pamUser:            "pam-preset-user-and-daemon-selected-broker",
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    gdmTestIgnoredMessage,
			},
		},
		"Error_during_broker_auth_mode_selection_if_UI_is_not_valid": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithUILayout(passwordUILayoutID, "", nil),
			),
			pamUser: "pam-preset-user-for-client-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{stage: proto.Stage_authModeSelection},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "invalid empty UI Layout information from broker",
			},
		},
		"Error_on_missing_authentication_modes": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetAuthenticationModesReturn([]*authd.GAMResponse_AuthenticationMode{}, nil),
			),
			pamUser: "pam-preset-user-for-client-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{stage: proto.Stage_authModeSelection},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrCredUnavail,
				msg:    "no supported authentication mode available for this provider",
			},
		},
		"Error_on_authentication_mode_selection": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithSelectAuthenticationModeReturn(nil, errors.New("error selecting auth mode")),
			),
			pamUser: "pam-preset-user-for-client-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{stage: proto.Stage_authModeSelection},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "can't select authentication mode: error selecting auth mode",
			},
		},
		"Error_on_invalid_auth-mode_layout_type": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithSelectAuthenticationModeReturn(&authd.UILayout{
					Type: "invalid layout",
				}, nil),
			),
			pamUser: "pam-preset-user-with-client-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{stage: proto.Stage_challenge},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    `Sending GDM event failed: Conversation error: unknown layout type: "invalid layout"`,
			},
		},
		"Error_on_authentication_client_failure": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedReturn(nil, errors.New("some authentication error")),
			),
			pamUser: "pam-preset-user-for-client-selected-broker",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
					Secret: "gdm-password",
				}},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
			},
			wantStage: proto.Stage_challenge,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "some authentication error",
			},
		},
		"Error_on_authentication_client_invalid_message": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: auth.Denied,
					Msg:    "invalid JSON",
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker",
			messages: []tea.Msg{
				gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
					Secret: "gdm-good-password",
				}},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "invalid json data from provider: invalid character 'i' looking for beginning of value",
			},
		},
		"Error_on_authentication_client_denied_because_of_wrong_password_-_with_error_message": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
				pam_test.WithIsAuthenticatedMessage("you're not allowed!"),
			),
			pamUser: "pam-preset-user-for-client-selected-brokers-with-wrong-pass",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_authModeSelection,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-wrong-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent,
			},
			wantStage: gdmTestIgnoreStage,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Denied,
				Msg:    "you're not allowed!",
			}},
			wantExitStatus: pamError{
				status: pam.ErrAuth,
				msg:    "you're not allowed!",
			},
		},
		"Error_on_authentication_client_denied_because_of_wrong_password": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
			),
			pamUser: "pam-preset-user-and-client-selected-broker-with-wrong-pass",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_brokerSelection,
					events: []*gdm.EventData{
						gdm_test.SelectBrokerEvent(firstBrokerInfo.Id),
					},
				},
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-wrong-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent,
			},
			wantStage:      gdmTestIgnoreStage,
			wantGdmAuthRes: []*authd.IAResponse{{Access: auth.Denied}},
			wantExitStatus: pamError{
				status: pam.ErrAuth,
				msg:    "Access denied",
			},
		},
		"Error_on_authentication_client_denied_because_of_wrong_password_after_retry": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedWantSecret("gdm-good-password"),
				pam_test.WithIsAuthenticatedMaxRetries(1),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker-with-wrong-pass",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReadyFull{
							authData: &authd.IARequest_AuthenticationData_Secret{
								Secret: "gdm-wrong-password",
							},
							commands: []tea.Cmd{
								sendEvent(gdmTestSendAuthDataWhenReady{
									&authd.IARequest_AuthenticationData_Secret{
										Secret: "gdm-another-wrong-password",
									},
								}),
							},
						}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent, // retry
				gdm.EventType_authEvent, // denied
			},
			wantStage: gdmTestIgnoreStage,
			wantGdmAuthRes: []*authd.IAResponse{
				{Access: auth.Retry},
				{Access: auth.Denied},
			},
			wantExitStatus: pamError{
				status: pam.ErrAuth,
				msg:    "Access denied",
			},
		},
		"Error_on_authentication_client_because_of_empty_auth_data_access": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker-with-wrong-pass",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-some-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent, // denied
			},
			wantStage: gdmTestIgnoreStage,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Denied,
				Msg:    `Access "" is not valid`,
			}},
			wantExitStatus: pamError{
				status: pam.ErrAuth,
				msg:    `Access "" is not valid`,
			},
		},
		"Error_on_authentication_client_because_of_invalid_auth_data_access_with_message": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil),
				pam_test.WithIsAuthenticatedReturn(&authd.IAResponse{
					Access: "no way you get here!",
					Msg:    `{"message": "This is not a valid access"}`,
				}, nil),
			),
			pamUser: "pam-preset-user-and-daemon-selected-broker-with-wrong-pass",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					commands: []tea.Cmd{
						sendEvent(gdmTestSendAuthDataWhenReady{&authd.IARequest_AuthenticationData_Secret{
							Secret: "gdm-some-password",
						}}),
					},
				},
			},
			wantSelectedBroker: firstBrokerInfo.Id,
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
				gdm.RequestType_changeStage, // -> authMode Selection
				gdm.RequestType_changeStage, // -> password
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokersReceived,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent, // denied
			},
			wantStage: gdmTestIgnoreStage,
			wantGdmAuthRes: []*authd.IAResponse{{
				Access: auth.Denied,
				Msg:    `Access "no way you get here!" is not valid`,
			}},
			wantExitStatus: pamError{
				status: pam.ErrAuth,
				msg:    `Access "no way you get here!" is not valid`,
			},
		},
		"Error_on_change_stage_using_an_unknown_stage": {
			gdmEvents: []*gdm.EventData{
				gdm_test.ChangeStageEvent(gdmTestIgnoreStage),
			},
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
			},
			wantNoGdmRequests: []gdm.RequestType{
				gdm.RequestType_changeStage,
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_brokersReceived,
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected,
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_uiLayoutReceived,
				gdm.EventType_authEvent,
			},
			wantStage: gdmTestIgnoreStage,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    `unknown PAM stage: "-1"`,
			},
		},
		"Error_during_hello_conversation": {
			convError: map[string]error{
				gdm_test.DataToJSON(t, &gdm.Data{
					Type: gdm.DataType_hello,
				}): errors.New("this is an hello error"),
			},
			wantExitStatus: pamError{
				status: pam.ErrCredUnavail,
				msg:    "GDM initialization failed: Conversation error: this is an hello error",
			},
		},
		"Error_during_hello_on_protocol_mismatch": {
			protoVersion: 99999999,
			wantExitStatus: pamError{
				status: pam.ErrCredUnavail,
				msg:    "GDM protocol initialization failed, type hello, version 99999999",
			},
		},
		"Error_during_poll": {
			convError: map[string]error{
				gdm_test.DataToJSON(t, &gdm.Data{Type: gdm.DataType_poll}): errors.New("this is a poll error"),
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "Sending GDM poll failed: Conversation error: this is a poll error",
			},
		},
		"Error_on_change_stage": {
			convError: map[string]error{
				gdm_test.DataToJSON(t, &gdm.Data{
					Type: gdm.DataType_request,
					Request: &gdm.RequestData{
						Type: gdm.RequestType_changeStage,
						Data: &gdm.RequestData_ChangeStage{
							ChangeStage: &gdm.Requests_ChangeStage{
								Stage: proto.Stage_brokerSelection,
							},
						},
					},
				}): errors.New("this is a stage change error"),
			},
			gdmEvents: []*gdm.EventData{
				gdm_test.ChangeStageEvent(proto.Stage_brokerSelection),
			},
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "Changing GDM stage failed: Conversation error: this is a stage change error",
			},
		},
		"Error_on_request_UI_capabilities": {
			convError: map[string]error{
				gdm_test.DataToJSON(t, &gdm.Data{
					Type: gdm.DataType_request,
					Request: &gdm.RequestData{
						Type: gdm.RequestType_uiLayoutCapabilities,
						Data: &gdm.RequestData_UiLayoutCapabilities{},
					},
				}): errors.New("this is an UI capabilities request error"),
			},
			wantNoBrokers: true,
			wantExitStatus: pamError{
				status: pam.ErrSystem,
				msg:    "Sending GDM UI capabilities Request failed: Conversation error: this is an UI capabilities request error",
			},
		},
		"Error_on_selecting_user_name_after_PAM_provided_already_one": {
			clientOptions: append(slices.Clone(singleBrokerClientOptions),
				pam_test.WithGetPreviousBrokerReturn(firstBrokerInfo.Id, nil)),
			pamUser: "gdm-pam-preset-user",
			messages: []tea.Msg{
				gdmTestWaitForStage{
					stage: proto.Stage_challenge,
					events: []*gdm.EventData{
						gdm_test.ChangeStageEvent(proto.Stage_userSelection),
					},
					commands: []tea.Cmd{
						sendEvent(gdmTestWaitForStage{
							stage: proto.Stage_userSelection,
							events: []*gdm.EventData{
								gdm_test.SelectUserEvent("another-selected-user"),
							},
						}),
					},
				},
			},
			wantGdmRequests: []gdm.RequestType{
				gdm.RequestType_uiLayoutCapabilities,
				gdm.RequestType_changeStage, // -> broker Selection
			},
			wantGdmEvents: []gdm.EventType{
				gdm.EventType_userSelected, // First selection, done from PAM.
				gdm.EventType_userSelected, // Second injected (and refused) selection.
			},
			wantMessages: []tea.Msg{
				userSelected{"gdm-pam-preset-user"},
				userSelected{"another-selected-user"},
			},
			wantNoGdmEvents: []gdm.EventType{
				gdm.EventType_brokerSelected,
				gdm.EventType_authModesReceived,
				gdm.EventType_authModeSelected,
				gdm.EventType_authEvent,
			},
			wantStage: proto.Stage_userSelection,
			wantExitStatus: pamError{
				status: pam.ErrPermDenied,
				msg:    `Changing username "gdm-pam-preset-user" to "another-selected-user" is not allowed`,
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.clientOptions == nil {
				tc.clientOptions = singleBrokerClientOptions
			}
			if tc.client == nil {
				tc.client = pam_test.NewDummyClient(gdmTestPrivateKey, tc.clientOptions...)
			}
			if tc.sessionMode == authd.SessionMode_UNDEFINED {
				tc.sessionMode = authd.SessionMode_LOGIN
			}

			messagesToSend := tc.messages
			messagesToWait := append(tc.messages, tc.wantMessages...)

			if tc.wantExitStatus != gdmTestEarlyStopExitStatus {
				messagesToWait = append(messagesToWait, tc.wantExitStatus)
			}

			gdmMutex := sync.Mutex{}
			gdmHandler := &gdmConvHandler{
				t:                    t,
				mu:                   &gdmMutex,
				protoVersion:         gdm.ProtoVersion,
				convError:            tc.convError,
				currentStageChanged:  *sync.NewCond(&gdmMutex),
				pendingEventsFlushed: make(chan struct{}),
				allRequestsReceived:  make(chan struct{}),
				allEventsReceived:    make(chan struct{}),
				startAuthRequested:   make(chan struct{}),
				supportedLayouts:     tc.supportedLayouts,
				pendingEvents:        tc.gdmEvents,
				wantEvents:           tc.wantGdmEvents,
				wantRequests:         tc.wantGdmRequests,
			}

			var exitStatus PamReturnStatus
			uiModel := newUIModelForClients(pam_test.NewModuleTransactionDummy(gdmHandler),
				Gdm, tc.sessionMode, tc.client, nil, &exitStatus)

			appState := gdmTestUIModel{
				uiModel:             uiModel,
				gdmHandler:          gdmHandler,
				wantMessages:        slices.Clone(messagesToWait),
				wantMessagesHandled: make(chan struct{}),
			}

			if tc.supportedLayouts == nil {
				gdmHandler.supportedLayouts = []*authd.UILayout{
					pam_test.FormUILayout(pam_test.WithWait(true)),
				}
			}

			if tc.protoVersion != 0 {
				gdmHandler.protoVersion = tc.protoVersion
			}

			if tc.pamUser != "" {
				require.NoError(t, uiModel.pamMTx.SetItem(pam.User, tc.pamUser))
			}
			if tc.pamUser != "" && tc.wantUsername == "" {
				tc.wantUsername = tc.pamUser
			}

			teaOpts, err := TeaHeadlessOptions()
			require.NoError(t, err, "Setup: Can't setup bubble tea options")
			teaOpts = append(teaOpts, tea.WithFilter(appState.filterFunc))
			p := tea.NewProgram(&appState, teaOpts...)
			appState.program = p

			controlDone := make(chan struct{})
			go func() {
				wg := sync.WaitGroup{}
				if len(messagesToWait) > 0 {
					for _, m := range messagesToSend {
						t.Logf("Sent message %#v\n", m)
						p.Send(m)
					}
					wg.Add(1)
					go func() {
						t.Log("Waiting for wantMessagesHandled", messagesToWait)
						<-appState.wantMessagesHandled
						t.Log("DONE waiting for wantMessagesHandled")
						wg.Done()
					}()
				}
				if len(tc.gdmEvents) > 0 {
					wg.Add(1)
					go func() {
						t.Log("Waiting for pendingEventsFlushed")
						<-gdmHandler.pendingEventsFlushed
						t.Log("DONE waiting for pendingEventsFlushed")
						wg.Done()
					}()
				}
				if len(tc.wantGdmRequests) > 0 {
					wg.Add(1)
					go func() {
						t.Log("Waiting for allRequestsReceived")
						<-gdmHandler.allRequestsReceived
						wg.Done()
						t.Log("DONE waiting for allRequestsReceived")
					}()
				}
				if len(tc.wantGdmEvents) > 0 {
					wg.Add(1)
					go func() {
						t.Log("Waiting for allEventsReceived")
						<-gdmHandler.allEventsReceived
						wg.Done()
						t.Log("DONE waiting for allEventsReceived")
					}()
				}

				t.Log("Waiting for expected events")
				if tc.timeout == 0 {
					tc.timeout = 20 * time.Second
				}
				waitChan := make(chan struct{})
				go func() {
					wg.Wait()
					close(waitChan)
				}()
				select {
				case <-time.After(time.Duration(testutils.SleepMultiplier() *
					float64(tc.timeout))):
					t.Log("Timeout waiting for expected events")
				case <-waitChan:
				}
				t.Log("Waiting for events done...")

				// Ensure we've nothing to send back...
				select {
				case <-time.After(gdmPollFrequency * 2):
					// All good, it seems there's nothing coming.
				case <-gdmHandler.pendingEventsFlushed:
				}
				t.Log("Waiting for flushing events done...")

				defer close(controlDone)
				t.Log("Time to quit!")
				appState.programShouldQuit.Store(true)
				p.Send(tea.Quit())
			}()
			_, err = p.Run()
			require.NoError(t, err)

			logStatus := func() {
				appState.mu.Lock()
				defer appState.mu.Unlock()
				gdmHandler.mu.Lock()
				defer gdmHandler.mu.Unlock()

				receivedEventTypes := []gdm.EventType{}
				for _, e := range gdmHandler.receivedEvents {
					receivedEventTypes = append(receivedEventTypes, e.Type)
				}

				t.Log("----------------")
				t.Logf("Remaining msgs: %#v\n", appState.wantMessages)
				t.Log("----------------")
				t.Logf("Received events: %#v\n", receivedEventTypes)
				t.Logf("Wanted events: %#v\n", tc.wantGdmEvents)
				t.Log("----------------")
				t.Logf("Handled requests: %#v\n", gdmHandler.handledRequests)
				t.Logf("Wanted requests: %#v\n", tc.wantGdmRequests)
				t.Log("----------------")
			}

			select {
			case <-time.After(time.Duration(testutils.SleepMultiplier()*5) * time.Second):
				logStatus()
				t.Fatalf("timeout waiting for test expected results")
			case <-controlDone:
			}

			gdmHandler.mu.Lock()
			defer gdmHandler.mu.Unlock()

			appState.mu.Lock()
			defer appState.mu.Unlock()

			if tc.wantExitStatus.Message() == gdmTestIgnoredMessage {
				switch wantRet := tc.wantExitStatus.(type) {
				case PamReturnError:
					exitErr, ok := exitStatus.(PamReturnError)
					require.True(t, ok, "exit status should be an error")
					require.Equal(t, wantRet.Status(), exitErr.Status())
				case PamSuccess:
					_, ok := exitStatus.(PamSuccess)
					require.True(t, ok, "exit status should be a success")
				default:
					t.Fatalf("Unexpected exit status: %v", wantRet)
				}
			} else {
				require.Equal(t, tc.wantExitStatus, exitStatus)
			}

			require.True(t, appState.gdmModel.conversationsStopped,
				"GDM conversation should be stopped")

			for _, req := range tc.wantNoGdmRequests {
				require.NotContains(t, gdmHandler.handledRequests, req,
					"GDM requests %v contains unexpected request %q",
					stringifySlice(gdmHandler.handledRequests), req)
			}

			if tc.wantStage != gdmTestIgnoreStage {
				require.Equal(t, tc.wantStage, gdmHandler.currentStage,
					"GDM Stage %q does not match with expected one %q",
					gdmHandler.currentStage, tc.wantStage)
			}

			for _, req := range tc.wantGdmRequests {
				// We don't do full equal check since we only care about having received
				// the ones explicitly listed.
				require.Contains(t, gdmHandler.handledRequests, req)
			}

			receivedEventTypes := []gdm.EventType{}
			for _, e := range gdmHandler.receivedEvents {
				receivedEventTypes = append(receivedEventTypes, e.Type)
			}
			require.True(t, isSupersetOf(receivedEventTypes, tc.wantGdmEvents),
				"Required events have not been received: %v vs %v",
				stringifySlice(tc.wantGdmEvents), stringifySlice(receivedEventTypes))

			require.Empty(t, appState.wantMessages, "Wanted messages have not all been processed")

			username, err := appState.pamMTx.GetItem(pam.User)
			require.NoError(t, err, "Failed to get the PAM user name")
			// authd uses lowercase usernames
			require.Equal(t, strings.ToLower(tc.wantUsername), username,
				"User name does not match")

			gdm_test.RequireEqualData(t, tc.wantGdmAuthRes, gdmHandler.authEvents)

			if r, ok := tc.wantExitStatus.(PamReturnError); ok {
				// If the model exited with error and that matches, we don't
				// care much comparing all the expectations, since the final exit status
				// is matching what we expect.
				switch r.Status() {
				case pam.ErrIgnore, pam.ErrAuth:
				default:
					if r != gdmTestEarlyStopExitStatus {
						return
					}
					if slices.Contains(tc.messages, tea.Quit()) {
						// Test required to quit, so we can't be reliable on what the status is.
						return
					}
				}
			}

			wantBrokers := []*authd.ABResponse_BrokerInfo(nil)
			if !tc.wantNoBrokers {
				availableBrokers, err := appState.client.AvailableBrokers(context.TODO(), nil)
				require.NoError(t, err)
				wantBrokers = availableBrokers.GetBrokersInfos()
			}

			gdm_test.RequireEqualData(t, wantBrokers, gdmHandler.receivedBrokers)
			require.Equal(t, tc.wantSelectedBroker, gdmHandler.selectedBrokerID)
		})
	}
}

func TestMain(m *testing.M) {
	var err error
	gdmTestPrivateKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("could not create an valid rsa key: %v", err))
	}
	defer pam_test.MaybeDoLeakCheck()

	m.Run()
}
