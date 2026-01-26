// Package pam implements the pam grpc service protocol to the daemon.
package pam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"strings"

	"github.com/canonical/authd/internal/brokers"
	"github.com/canonical/authd/internal/brokers/auth"
	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/internal/services/permissions"
	"github.com/canonical/authd/internal/users"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ authd.PAMServer = Service{}

// Service is the implementation of the PAM module service.
type Service struct {
	userManager       *users.Manager
	brokerManager     *brokers.Manager
	permissionManager *permissions.Manager

	authd.UnimplementedPAMServer
}

// NewService returns a new PAM GRPC service.
func NewService(ctx context.Context, userManager *users.Manager, brokerManager *brokers.Manager, permissionManager *permissions.Manager) Service {
	log.Debug(ctx, "Building new gRPC PAM service")

	return Service{
		userManager:       userManager,
		brokerManager:     brokerManager,
		permissionManager: permissionManager,
	}
}

// AvailableBrokers returns the list of all brokers with their details.
func (s Service) AvailableBrokers(ctx context.Context, _ *authd.Empty) (*authd.ABResponse, error) {
	var r authd.ABResponse

	for _, b := range s.brokerManager.AvailableBrokers() {
		r.BrokersInfos = append(r.BrokersInfos, &authd.ABResponse_BrokerInfo{
			Id:        b.ID,
			Name:      b.Name,
			BrandIcon: &b.BrandIconPath,
		})
	}

	return &r, nil
}

// GetPreviousBroker returns the previous broker set for a given user, if any.
// If the user is not in our cache/database, it will try to check if it’s on the system, and return then "local".
func (s Service) GetPreviousBroker(ctx context.Context, req *authd.GPBRequest) (*authd.GPBResponse, error) {
	// authd usernames are lowercase
	username := strings.ToLower(req.GetUsername())

	// Use in memory cache first
	if b := s.brokerManager.BrokerForUser(username); b != nil {
		return &authd.GPBResponse{PreviousBroker: b.ID}, nil
	}

	// Load from database.
	brokerID, err := s.userManager.BrokerForUser(username)
	// User is not in our database.
	if err != nil && errors.Is(err, users.NoDataFoundError{}) {
		// FIXME: this part will not be here in the v2 API version, as we won’t have GetPreviousBroker and handle
		// autoselection silently in authd.
		// User not in database, if there is only the local broker available, return this one without saving it.
		if len(s.brokerManager.AvailableBrokers()) == 1 {
			log.Debugf(ctx, "GetPreviousBroker: User %q not found in database and only local broker available, selecting local broker", req.GetUsername())
			return &authd.GPBResponse{PreviousBroker: brokers.LocalBrokerName}, nil
		}

		// User not accessible through NSS, first time login or no valid user. Anyway, no broker selected.
		if _, err := user.Lookup(username); err != nil {
			log.Debugf(ctx, "GetPreviousBroker: User %q not found", username)
			return &authd.GPBResponse{}, nil
		}

		// We could resolve the user through NSS, which means then that another non authd service
		// service (passwd, winbind, sss…) is handling that user.
		brokerID = brokers.LocalBrokerName
	} else if err != nil {
		log.Infof(ctx, "GetPreviousBroker: Could not get broker for user %q from database: %v", username, err)
		return &authd.GPBResponse{}, nil
	}

	// No error but the brokerID is empty (broker in database but default broker not stored yet due no successful login)
	if brokerID == "" {
		log.Infof(ctx, "GetPreviousBroker: No broker set for user %q, letting the user select a new one", username)
		return &authd.GPBResponse{}, nil
	}

	// Check if the broker still exists. Its config file might have been removed from the config directory after the
	// user logged in the last time, in which case we let the user select a new broker.
	if !s.brokerManager.BrokerExists(brokerID) {
		log.Warningf(ctx, "GetPreviousBroker: Broker %q set for user %q does not exist, letting the user select a new one", brokerID, req.GetUsername())
		return &authd.GPBResponse{}, nil
	}

	// Cache the broker which should be used for the user, so that we don't have to query the database again next time -
	// except if the broker is the local broker, because then the decision to use the local broker should be made each
	// time the user tries to log in, based on whether the user is provided by any other NSS service.
	if brokerID == brokers.LocalBrokerName {
		return &authd.GPBResponse{PreviousBroker: brokerID}, nil
	}
	if err = s.brokerManager.SetDefaultBrokerForUser(brokerID, username); err != nil {
		log.Warningf(ctx, "GetPreviousBroker: Could not cache broker %q for user %q: %v", brokerID, username, err)
		return &authd.GPBResponse{}, nil
	}

	return &authd.GPBResponse{
		PreviousBroker: brokerID,
	}, nil
}

// SelectBroker starts a new session and selects the requested broker for the user.
func (s Service) SelectBroker(ctx context.Context, req *authd.SBRequest) (resp *authd.SBResponse, err error) {
	// authd usernames are lowercase
	username := strings.ToLower(req.GetUsername())
	brokerID := req.GetBrokerId()
	lang := req.GetLang()

	if username == "" {
		log.Errorf(ctx, "SelectBroker: No user name provided")
		return nil, status.Error(codes.InvalidArgument, "no user name provided")
	}
	if brokerID == "" {
		log.Errorf(ctx, "SelectBroker: No broker selected")
		return nil, status.Error(codes.InvalidArgument, "no broker selected")
	}
	if lang == "" {
		lang = "C"
	}

	var mode string
	switch req.GetMode() {
	case authd.SessionMode_LOGIN:
		mode = auth.SessionModeLogin
	case authd.SessionMode_CHANGE_PASSWORD:
		mode = auth.SessionModeChangePassword
	default:
		log.Errorf(ctx, "SelectBroker: Invalid session mode %q", req.GetMode())
		return nil, status.Error(codes.InvalidArgument, "invalid session mode")
	}

	// Create a session and Memorize selected broker for it.
	sessionID, encryptionKey, err := s.brokerManager.NewSession(brokerID, username, lang, mode)
	if err != nil {
		log.Errorf(ctx, "SelectBroker: Could not create session for user %q with broker %q: %v", username, brokerID, err)
		return nil, err
	}

	return &authd.SBResponse{
		SessionId:     sessionID,
		EncryptionKey: encryptionKey,
	}, nil
}

// GetAuthenticationModes fetches a list of authentication modes supported by the broker depending on the session information.
func (s Service) GetAuthenticationModes(ctx context.Context, req *authd.GAMRequest) (resp *authd.GAMResponse, err error) {
	defer decorate.OnError(&err, "could not get authentication modes")

	sessionID := req.GetSessionId()
	if sessionID == "" {
		log.Errorf(ctx, "GetAuthenticationModes: No session ID provided")
		return nil, status.Error(codes.InvalidArgument, "no session ID provided")
	}

	broker, err := s.brokerManager.BrokerFromSessionID(sessionID)
	if err != nil {
		log.Errorf(ctx, "GetAuthenticationModes: Could not get broker for session %q: %v", sessionID, err)
		return nil, err
	}

	var supportedLayouts []map[string]string
	for _, l := range req.GetSupportedUiLayouts() {
		layout, err := uiLayoutToMap(l)
		if err != nil {
			log.Errorf(ctx, "GetAuthenticationModes: Invalid UI layout %v: %v", l, err)
			return nil, err
		}
		supportedLayouts = append(supportedLayouts, layout)
	}

	authenticationModes, msg, err := broker.GetAuthenticationModes(ctx, sessionID, supportedLayouts)
	if err != nil {
		log.Errorf(ctx, "GetAuthenticationModes: Could not get authentication modes for session %q: %v", sessionID, err)
		return nil, err
	}

	var authModes []*authd.GAMResponse_AuthenticationMode
	for _, a := range authenticationModes {
		authModes = append(authModes, &authd.GAMResponse_AuthenticationMode{
			Id:    a[layouts.ID],
			Label: a[layouts.Label],
		})
	}

	var msgPtr *string
	if msg != "" {
		msgPtr = &msg
	}

	return &authd.GAMResponse{
		AuthenticationModes: authModes,
		Msg:                 msgPtr,
	}, nil
}

// SelectAuthenticationMode set given authentication mode as selected for this sessionID to the broker.
func (s Service) SelectAuthenticationMode(ctx context.Context, req *authd.SAMRequest) (resp *authd.SAMResponse, err error) {
	defer decorate.OnError(&err, "can't select authentication mode")

	sessionID := req.GetSessionId()
	authenticationModeID := req.GetAuthenticationModeId()

	if sessionID == "" {
		log.Errorf(ctx, "SelectAuthenticationMode: No session ID provided")
		return nil, status.Error(codes.InvalidArgument, "no session ID provided")
	}
	if authenticationModeID == "" {
		log.Errorf(ctx, "SelectAuthenticationMode: No authentication mode provided")
		return nil, status.Error(codes.InvalidArgument, "no authentication mode provided")
	}

	broker, err := s.brokerManager.BrokerFromSessionID(sessionID)
	if err != nil {
		log.Errorf(ctx, "SelectAuthenticationMode: Could not get broker for session %q: %v", sessionID, err)
		return nil, err
	}

	uiLayoutInfo, err := broker.SelectAuthenticationMode(ctx, sessionID, authenticationModeID)
	if err != nil {
		log.Errorf(ctx, "SelectAuthenticationMode: Could not select authentication mode %q for session %q: %v", authenticationModeID, sessionID, err)
		return nil, err
	}

	return &authd.SAMResponse{
		UiLayoutInfo: mapToUILayout(uiLayoutInfo),
	}, nil
}

// IsAuthenticated returns broker answer to authentication request.
func (s Service) IsAuthenticated(ctx context.Context, req *authd.IARequest) (resp *authd.IAResponse, err error) {
	sessionID := req.GetSessionId()
	if sessionID == "" {
		log.Errorf(ctx, "IsAuthenticated: No session ID provided")
		return nil, status.Error(codes.InvalidArgument, "no session ID provided")
	}

	broker, err := s.brokerManager.BrokerFromSessionID(sessionID)
	if err != nil {
		log.Errorf(ctx, "IsAuthenticated: Could not get broker for session %q: %v", sessionID, err)
		return nil, err
	}

	authenticationDataJSON, err := protojson.Marshal(req.GetAuthenticationData())
	if err != nil {
		log.Errorf(ctx, "IsAuthenticated: Could not marshal authentication data for session %q: %v", sessionID, err)
		return nil, err
	}

	access, data, err := broker.IsAuthenticated(ctx, sessionID, string(authenticationDataJSON))
	if err != nil {
		log.Errorf(ctx, "IsAuthenticated: Could not check authentication for session %q: %v", sessionID, err)
		return nil, err
	}

	log.Debugf(ctx, "%s: Authentication result: %s", sessionID, access)

	if access != auth.Granted {
		return &authd.IAResponse{
			Access: access,
			Msg:    data,
		}, nil
	}

	var uInfo types.UserInfo
	if err := json.Unmarshal([]byte(data), &uInfo); err != nil {
		log.Errorf(ctx, "IsAuthenticated: Could not unmarshal user data for session %q: %v", sessionID, err)
		return nil, fmt.Errorf("user data from broker invalid: %v", err)
	}

	// authd uses lowercase user and group names
	uInfo.Name = strings.ToLower(uInfo.Name)
	for i, g := range uInfo.Groups {
		uInfo.Groups[i].Name = strings.ToLower(g.Name)
	}

	// Check if the user is locked. We can only do this after the broker has granted access, because we want to avoid
	// leaking whether a user exists or not to unauthenticated users.
	// TODO: We might want to let the broker know whether the user is locked or not, so that it can avoid storing any
	//       updated tokens or user info on disk.
	userIsLocked, err := s.userManager.IsUserLocked(uInfo.Name)
	if err != nil && !errors.Is(err, users.NoDataFoundError{}) {
		log.Errorf(ctx, "IsAuthenticated: Could not check if user %q is locked: %v", uInfo.Name, err)
		return nil, fmt.Errorf("could not check if user %q is locked: %w", uInfo.Name, err)
	}
	// Throw an error if the user trying to authenticate already exists in the database and is locked
	if err == nil && userIsLocked {
		log.Noticef(ctx, "Authentication failure: user %q is locked", uInfo.Name)
		return nil, status.Error(codes.PermissionDenied, fmt.Sprintf("user %s is locked", uInfo.Name))
	}

	// Update database and local groups on granted auth.
	if err := s.userManager.UpdateUser(uInfo); err != nil {
		log.Errorf(ctx, "IsAuthenticated: Could not update user %q in database: %v", uInfo.Name, err)
		return nil, err
	}

	return &authd.IAResponse{
		Access: access,
		Msg:    "",
	}, nil
}

// SetDefaultBrokerForUser sets the default broker for the given user.
func (s Service) SetDefaultBrokerForUser(ctx context.Context, req *authd.SDBFURequest) (empty *authd.Empty, err error) {
	defer decorate.OnError(&err, "can't set default broker %q for user %q", req.GetBrokerId(), req.GetUsername())

	// authd usernames are lowercase
	username := strings.ToLower(req.GetUsername())
	brokerID := req.GetBrokerId()

	if username == "" {
		log.Errorf(ctx, "SetDefaultBrokerForUser: No user name given")
		return nil, status.Error(codes.InvalidArgument, "no user name given")
	}

	// Don't allow setting the default broker to the local broker, because the decision to use the local broker should
	// be made each time the user tries to log in, based on whether the user is provided by any other NSS service.
	if brokerID == brokers.LocalBrokerName {
		log.Errorf(ctx, "SetDefaultBrokerForUser: Can't set local broker as default for user %q", username)
		return nil, status.Error(codes.InvalidArgument, "can't set local broker as default")
	}

	if err = s.brokerManager.SetDefaultBrokerForUser(brokerID, username); err != nil {
		log.Errorf(ctx, "SetDefaultBrokerForUser: Could not set default broker %q for user %q: %v", brokerID, username, err)
		return &authd.Empty{}, err
	}

	if err = s.userManager.UpdateBrokerForUser(username, brokerID); err != nil {
		log.Errorf(ctx, "SetDefaultBrokerForUser: Could not update broker for user %q in database: %v", username, err)
		return &authd.Empty{}, err
	}

	return &authd.Empty{}, nil
}

// EndSession asks the broker associated with the sessionID to end the session.
func (s Service) EndSession(ctx context.Context, req *authd.ESRequest) (empty *authd.Empty, err error) {
	defer decorate.OnError(&err, "could not abort session")

	sessionID := req.GetSessionId()
	if sessionID == "" {
		log.Errorf(ctx, "EndSession: No session ID given")
		return nil, status.Error(codes.InvalidArgument, "no session id given")
	}

	return &authd.Empty{}, s.brokerManager.EndSession(sessionID)
}

func uiLayoutToMap(layout *authd.UILayout) (mapLayout map[string]string, err error) {
	if layout.GetType() == "" {
		return nil, fmt.Errorf("invalid layout option: type is required, got: %v", layout)
	}
	r := map[string]string{layouts.Type: layout.GetType()}
	if l := layout.GetLabel(); l != "" {
		r[layouts.Label] = l
	}
	if b := layout.GetButton(); b != "" {
		r[layouts.Button] = b
	}
	if w := layout.GetWait(); w != "" {
		r[layouts.Wait] = w
	}
	if e := layout.GetEntry(); e != "" {
		r[layouts.Entry] = e
	}
	if c := layout.GetContent(); c != "" {
		r[layouts.Content] = c
	}
	if c := layout.GetCode(); c != "" {
		r[layouts.Code] = c
	}

	if layout.GetType() != layouts.QrCode {
		return r, nil
	}

	r[layouts.RendersQrCode] = layouts.False
	if rc := layout.RendersQrcode; rc == nil || *rc {
		// If the field is not set, we keep retro-compatibility with what we were
		// dong before of the addition of the field.
		r[layouts.RendersQrCode] = layouts.True
	}
	return r, nil
}

// mapToUILayout generates an UILayout from the input map.
func mapToUILayout(layout map[string]string) (r *authd.UILayout) {
	typ := layout[layouts.Type]
	label := layout[layouts.Label]
	entry := layout[layouts.Entry]
	button := layout[layouts.Button]
	wait := layout[layouts.Wait]
	content := layout[layouts.Content]
	code := layout[layouts.Code]

	// We don't return whether the qrcode rendering is enabled back to the
	// client on purpose, since it's something it mandates.

	return &authd.UILayout{
		Type:    typ,
		Label:   &label,
		Entry:   &entry,
		Button:  &button,
		Wait:    &wait,
		Content: &content,
		Code:    &code,
	}
}
