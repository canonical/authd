package testutils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/canonical/authd/internal/brokers/layouts"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	dbusInterface = "com.ubuntu.authd.Broker"
	objectPathFmt = "/com/ubuntu/authd/%s"
	nameFmt       = "com.ubuntu.authd.%s"

	// IDSeparator is the value used to append values to the sessionID in the broker mock.
	IDSeparator = "_separator_"
)

const (
	// authGranted is the response when the authentication is granted.
	authGranted = "granted"
	// authDenied is the response when the authentication is denied.
	authDenied = "denied"
	// authCancelled is the response when the authentication is cancelled.
	authCancelled = "cancelled"
	// authRetry is the response when the authentication needs to be retried (another chance).
	authRetry = "retry"
	// authNext is the response when another MFA (including changing password) authentication is necessary.
	authNext = "next"
)

var brokerConfigTemplate = `[authd]
name = %s
brand_icon = mock_icon.png
dbus_name = com.ubuntu.authd.%s
dbus_object = /com/ubuntu/authd/%s
`

type isAuthenticatedCtx struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// BrokerBusMock is the D-Bus object that will answer calls for the broker mock.
type BrokerBusMock struct {
	name                   string
	isAuthenticatedCalls   map[string]isAuthenticatedCtx
	isAuthenticatedCallsMu sync.RWMutex
}

// StartBusBrokerMock starts the D-Bus service and exports it on the system bus.
// It returns the configuration file path for the exported broker.
func StartBusBrokerMock(cfgDir string, brokerName string) (string, func(), error) {
	busObjectPath := fmt.Sprintf(objectPathFmt, brokerName)
	busName := fmt.Sprintf(nameFmt, brokerName)

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return "", nil, err
	}

	bus := BrokerBusMock{
		name:                   brokerName,
		isAuthenticatedCalls:   map[string]isAuthenticatedCtx{},
		isAuthenticatedCallsMu: sync.RWMutex{},
	}

	if err = conn.Export(&bus, dbus.ObjectPath(busObjectPath), dbusInterface); err != nil {
		conn.Close()
		return "", nil, err
	}

	err = conn.Export(introspect.NewIntrospectable(&introspect.Node{
		Name: busObjectPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{
				Name:    dbusInterface,
				Methods: introspect.Methods(&bus),
			},
		},
	}), dbus.ObjectPath(busObjectPath), introspect.IntrospectData.Name)
	if err != nil {
		conn.Close()
		return "", nil, err
	}

	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("can't get the D-Bus name %s: %w", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return "", nil, errors.New("not a D-Bus primary name owner")
	}

	configPath, err := writeConfig(cfgDir, brokerName)
	if err != nil {
		conn.Close()
		return "", nil, err
	}

	return configPath, func() {
		_, _ = conn.ReleaseName(busName)
		_ = conn.Close()
	}, nil
}

func writeConfig(cfgDir, name string) (string, error) {
	cfgPath := filepath.Join(cfgDir, name+".conf")
	s := fmt.Sprintf(brokerConfigTemplate, name, name, name, name)
	if err := os.WriteFile(cfgPath, []byte(s), 0600); err != nil {
		return "", err
	}
	return cfgPath, nil
}

// NewSession returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) NewSession(username, lang, mode string) (sessionID, encryptionKey string, dbusErr *dbus.Error) {
	parsedUsername := parseSessionID(username)
	if parsedUsername == "ns_error" {
		return "", "", dbus.MakeFailedError(fmt.Errorf("broker %q: NewSession errored out", b.name))
	}
	if parsedUsername == "ns_no_id" {
		return "", username + "_key", nil
	}
	return GenerateSessionID(username), GenerateEncryptionKey(b.name), nil
}

// GetAuthenticationModes returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) GetAuthenticationModes(sessionID string, supportedUILayouts []map[string]string) (authenticationModes []map[string]string, dbusErr *dbus.Error) {
	sessionID = parseSessionID(sessionID)
	switch sessionID {
	case "gam_invalid":
		return []map[string]string{
			{"invalid": "invalid"},
		}, nil
	case "gam_empty":
		return nil, nil
	case "gam_error":
		return nil, dbus.MakeFailedError(fmt.Errorf("broker %q: GetAuthenticationModes errored out", b.name))
	case "gam_multiple_modes":
		return []map[string]string{
			{layouts.ID: "mode1", layouts.Label: "Mode 1"},
			{layouts.ID: "mode2", layouts.Label: "Mode 2"},
		}, nil
	default:
		return []map[string]string{
			{layouts.ID: "mode1", layouts.Label: "Mode 1"},
		}, nil
	}
}

// SelectAuthenticationMode returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) SelectAuthenticationMode(sessionID, authenticationModeName string) (uiLayoutInfo map[string]string, dbusErr *dbus.Error) {
	sessionID = parseSessionID(sessionID)
	switch sessionID {
	case "sam_success_required_entry":
		return map[string]string{
			layouts.Type:  "required-entry",
			layouts.Entry: "entry_type",
		}, nil
	case "sam_success_optional_entry":
		return map[string]string{
			layouts.Type:  "optional-entry",
			layouts.Entry: "entry_type",
		}, nil
	case "sam_missing_optional_entry":
		return map[string]string{
			layouts.Type: "optional-entry",
		}, nil
	case "sam_invalid_layout_type":
		return map[string]string{
			"invalid": "invalid",
		}, nil
	case "sam_missing_required_entry":
		return map[string]string{
			layouts.Type: "required-entry",
		}, nil
	case "sam_invalid_required_entry":
		return map[string]string{
			layouts.Type:  "required-entry",
			layouts.Entry: "invalid entry",
		}, nil
	case "sam_invalid_optional_entry":
		return map[string]string{
			layouts.Type:  "optional-entry",
			layouts.Entry: "invalid entry",
		}, nil
	case "sam_unknown_field":
		return map[string]string{
			layouts.Type:    "required-entry",
			layouts.Entry:   "entry_type",
			"unknown_field": "unknown",
		}, nil
	case "sam_error":
		return nil, dbus.MakeFailedError(fmt.Errorf("broker %q: SelectAuthenticationMode errored out", b.name))
	case "sam_no_layout":
		return nil, nil
	case "sam_empty_layout":
		return map[string]string{}, nil
	}
	// Should never get here
	return map[string]string{}, dbus.MakeFailedError(fmt.Errorf("broker %q: unknown sessionID %q", b.name, sessionID))
}

// IsAuthenticated returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) IsAuthenticated(sessionID, authenticationData string) (access, data string, dbusErr *dbus.Error) {
	// The IsAuthenticated needs to function a bit differently to still allow tests to be executed in parallel.
	// We have to use both the prefixed sessionID and the parsed one in order to differentiate between test cases.
	parsedID := parseSessionID(sessionID)

	if parsedID == "ia_error" {
		return "", "", dbus.MakeFailedError(fmt.Errorf("broker %q: IsAuthenticated errored out", b.name))
	}

	// Handles the context that will be assigned for the IsAuthenticated handler
	b.isAuthenticatedCallsMu.RLock()
	if _, exists := b.isAuthenticatedCalls[sessionID]; exists {
		b.isAuthenticatedCallsMu.RUnlock()
		return "", "", dbus.MakeFailedError(fmt.Errorf("broker %q: IsAuthenticated already running for session %q", b.name, sessionID))
	}
	b.isAuthenticatedCallsMu.RUnlock()

	ctx, cancel := context.WithCancel(context.Background())
	b.isAuthenticatedCallsMu.Lock()
	b.isAuthenticatedCalls[sessionID] = isAuthenticatedCtx{ctx, cancel}
	b.isAuthenticatedCallsMu.Unlock()

	// Cleans the call after it's done
	defer func() {
		b.isAuthenticatedCallsMu.Lock()
		delete(b.isAuthenticatedCalls, sessionID)
		b.isAuthenticatedCallsMu.Unlock()
	}()

	access = authGranted
	data = fmt.Sprintf(`{"userinfo": %s}`, userInfoFromName(sessionID, nil))

	switch parsedID {
	case "ia_timeout":
		time.Sleep(time.Second)
		access = authDenied
		data = `{"message": "denied by time out"}`

	case "ia_wait":
		<-ctx.Done()
		access = authCancelled
		data = ""

	case "ia_second_call":
		select {
		case <-ctx.Done():
			access = authCancelled
			data = ""
		case <-time.After(2 * time.Second):
			access = authGranted
			data = fmt.Sprintf(`{"userinfo": %s}`, userInfoFromName(sessionID, nil))
		}

	case "ia_next":
		access = authNext
		data = ""

	case "success_with_local_groups":
		extragroups := []groupJSONInfo{{Name: "localgroup1"}, {Name: "localgroup3"}}
		data = fmt.Sprintf(`{"userinfo": %s}`, userInfoFromName(sessionID, extragroups))

	case "success_with_uppercase_groups":
		extragroups := []groupJSONInfo{
			{Name: "GROUP1", UGID: "12345678"},
			{Name: "GROUP2", UGID: "87654321"},
		}
		data = fmt.Sprintf(`{"userinfo": %s}`, userInfoFromName(sessionID, extragroups))

	case "ia_invalid_access":
		access = "invalid"

	case "ia_invalid_data":
		data = "invalid"

	case "ia_empty_data":
		data = ""

	case "ia_invalid_userinfo":
		data = `{"userinfo": "not valid"}`

	case "ia_denied_without_data":
		access = authDenied
		data = ""

	case "ia_retry_without_data":
		access = authRetry
		data = ""

	case "ia_next_with_data":
		access = authNext
		data = `{"message": "It's fine to show a message here"}`

	case "ia_next_with_invalid_data":
		access = authNext
		data = `{"msg": "there should not be a message here"}`

	case "ia_cancelled_with_data":
		access = authCancelled
		data = `{"message": "there should not be a message here"}`
	}

	return access, data, nil
}

// EndSession returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) EndSession(sessionID string) (dbusErr *dbus.Error) {
	sessionID = parseSessionID(sessionID)
	if sessionID == "es_error" {
		return dbus.MakeFailedError(fmt.Errorf("broker %q: EndSession errored out", b.name))
	}
	return nil
}

// CancelIsAuthenticated cancels an ongoing IsAuthenticated call if it exists.
func (b *BrokerBusMock) CancelIsAuthenticated(sessionID string) (dbusErr *dbus.Error) {
	b.isAuthenticatedCallsMu.Lock()
	defer b.isAuthenticatedCallsMu.Unlock()
	if _, exists := b.isAuthenticatedCalls[sessionID]; !exists {
		return nil
	}
	b.isAuthenticatedCalls[sessionID].cancelFunc()
	delete(b.isAuthenticatedCalls, sessionID)
	return nil
}

// UserPreCheck returns default values to be used in tests or an error if requested.
func (b *BrokerBusMock) UserPreCheck(username string) (userinfo string, dbusErr *dbus.Error) {
	if username != "user-pre-check" && username != "local-pre-check" {
		return "", dbus.MakeFailedError(fmt.Errorf("broker %q: UserPreCheck errored out", b.name))
	}
	return userInfoFromName(username, nil), nil
}

// parseSessionID is wrapper around the sessionID to remove some values appended during the tests.
//
// The sessionID can have multiple values appended to differentiate between subtests and avoid concurrency conflicts,
// and only the last value (i.e. "..._separator_ID-session_id") will be considered.
func parseSessionID(sessionID string) string {
	cut := strings.Split(sessionID, IDSeparator)
	if len(cut) == 0 {
		return ""
	}
	return strings.TrimSuffix(cut[len(cut)-1], "-session_id")
}

type groupJSONInfo struct {
	Name string
	UGID string
}

// userInfoFromName transform a given name to the strinfigy userinfo string.
func userInfoFromName(sessionID string, extraGroups []groupJSONInfo) string {
	// Default values
	parsedID := parseSessionID(sessionID)

	name := strings.TrimSuffix(sessionID, "-session_id")
	group := "group-" + parsedID
	home := "/home/" + parsedID
	shell := "/bin/sh/" + parsedID
	gecos := "gecos for " + parsedID
	ugid := "ugid-" + parsedID

	switch parsedID {
	case "ia_info_empty_user_name":
		name = ""
	case "ia_info_mismatching_user_name":
		name = "different_username"
	case "ia_info_empty_group_name":
		group = ""
	case "ia_info_empty_ugid":
		ugid = ""
	case "ia_info_empty_gecos":
		gecos = ""
	case "ia_info_empty_groups":
		group = "-"
	case "ia_info_invalid_home":
		home = "this is not a homedir"
	case "ia_info_invalid_shell":
		shell = "this is not a valid shell"
	case "local-pre-check":
		name = "root"
		home = "/root"
	}

	groups := []groupJSONInfo{{Name: group, UGID: ugid}}
	for _, g := range extraGroups {
		var ugid string
		if g.UGID != "" {
			ugid = g.UGID
		}
		groups = append(groups, groupJSONInfo{Name: g.Name, UGID: ugid})
	}

	if group == "-" {
		groups = []groupJSONInfo{}
	}

	user := struct {
		Name   string
		UUID   string
		Dir    string
		Shell  string
		Groups []groupJSONInfo
		Gecos  string
	}{Name: name, Dir: home, Shell: shell, Groups: groups, Gecos: gecos}

	// only used for tests, we can ignore the template execution error as the returned data will be failing.
	var buf bytes.Buffer
	_ = template.Must(template.New("").Parse(`{
		"name": "{{.Name}}",
		"uuid": "{{.UUID}}",
		"gecos": "{{.Gecos}}",
		"dir": "{{.Dir}}",
		"shell": "{{.Shell}}",
		"avatar": "avatar for {{.Name}}",
		"groups": [ {{range $index, $g := .Groups}}
			{{- if $index}}, {{end -}}
			{"name": "{{.Name}}", "ugid": "{{.UGID}}"}
		{{- end}} ]
	}`)).Execute(&buf, user)

	return buf.String()
}

// GenerateSessionID returns a sessionID that can be used in tests.
func GenerateSessionID(username string) string {
	return fmt.Sprintf("%s-session_id", username)
}

// GenerateEncryptionKey returns an encryption key that can be used in tests.
func GenerateEncryptionKey(brokerName string) string {
	return fmt.Sprintf("%s-key", brokerName)
}
