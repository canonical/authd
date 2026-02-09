package adapter

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canonical/authd/internal/proto/authd"
	"github.com/canonical/authd/log"
	"github.com/canonical/authd/pam/internal/gdm"
	"github.com/canonical/authd/pam/internal/gdm_test"
	"github.com/canonical/authd/pam/internal/proto"
	tea "github.com/charmbracelet/bubbletea"
)

var gdmTestSequentialMessages atomic.Int64

// gdmTestUIModel is an override of [uiModel] used for testing the module with gdm.
type gdmTestUIModel struct {
	uiModel

	mu sync.Mutex

	gdmHandler *gdmConvHandler

	wantMessages        []tea.Msg
	wantMessagesHandled chan struct{}

	program           *tea.Program
	programShouldQuit atomic.Bool
}

// Custom messages for testing the gdm model.

type gdmTestAddPollResultEvent struct {
	event *gdm.EventData
}

type gdmTestWaitForCommandsDone struct {
	seq int64
}

type gdmTestCommands struct {
	events   []*gdm.EventData
	commands []tea.Cmd
}

type gdmTestWaitForStage struct {
	stage    proto.Stage
	events   []*gdm.EventData
	commands []tea.Cmd
}

type gdmTestCommandsDone gdmTestCommands

type gdmTestSendAuthDataWhenReady struct {
	authData authd.IARequestAuthenticationDataItem
}

type gdmTestSendAuthDataWhenReadyFull struct {
	authData authd.IARequestAuthenticationDataItem
	commands []tea.Cmd
	events   []*gdm.EventData
}

var currentPkg = reflect.TypeOf(gdmTestUIModel{}).PkgPath()

func (m *gdmTestUIModel) maybeHandleWantMessageUnlocked(msg tea.Msg) {
	returnErrorMsg, isError := msg.(PamReturnError)

	idx := slices.IndexFunc(m.wantMessages, func(wm tea.Msg) bool {
		match := reflect.DeepEqual(wm, msg)
		if match {
			return true
		}
		if !isError {
			return false
		}
		pamErr, ok := wm.(PamReturnError)
		if !ok {
			return false
		}
		if pamErr.Message() != gdmTestIgnoredMessage {
			return false
		}
		return pamErr.Status() == returnErrorMsg.Status()
	})

	if idx < 0 {
		return
	}

	m.wantMessages = slices.Delete(m.wantMessages, idx, idx+1)
	if len(m.wantMessages) == 0 {
		close(m.wantMessagesHandled)
	}
}

func (m *gdmTestUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if log.IsLevelEnabled(log.DebugLevel) &&
		reflect.TypeOf(msg).PkgPath() == currentPkg {
		safeMessageDebug(msg)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	commands := []tea.Cmd{}

	um, cmd := m.uiModel.Update(msg)
	m.uiModel = convertTo[uiModel](um)
	commands = append(commands, cmd)

	switch msg := msg.(type) {
	case gdmTestAddPollResultEvent:
		m.gdmHandler.appendPollResultEvents(msg.event)

	case gdmTestWaitForStage:
		doneMsg := &gdmTestCommandsDone{commands: msg.commands, events: msg.events}
		if len(doneMsg.commands) > 0 {
			seq := gdmTestSequentialMessages.Add(1)
			doneCommandsMsg := gdmTestWaitForCommandsDone{seq: seq}
			doneMsg.commands = append(doneMsg.commands, sendEvent(doneCommandsMsg))
			m.wantMessages = append(m.wantMessages, doneCommandsMsg)
		}

		waitFunc := m.gdmHandler.waitForStageChange(msg.stage)
		if waitFunc == nil {
			commands = append(commands, sendEvent(doneMsg))
			break
		}

		m.wantMessages = append(m.wantMessages, doneMsg)

		go func() {
			log.Debugf(context.TODO(), "Waiting for stage reached: %#v", doneMsg)
			waitFunc()
			log.Debugf(context.TODO(), "Stage reached: %#v", doneMsg)

			m.program.Send(doneMsg)
		}()

	case gdmTestCommands:
		doneMsg := (*gdmTestCommandsDone)(&msg)
		m.wantMessages = append(m.wantMessages, doneMsg)
		commands = append(commands, msg.commands...)
		commands = append(commands, sendEvent(doneMsg))

	case *gdmTestCommandsDone:
		// FIXME: We can't just define msgCommands a sub-sequence as we used to
		// do since that's unreliable, as per a bubbletea bug:
		//  - https://github.com/charmbracelet/bubbletea/issues/847
		msgCommands := slices.Clone(msg.commands)
		if len(msg.events) > 0 {
			m.gdmHandler.appendPollResultEvents(msg.events...)
			// If we've events as poll results, let's wait for a polling cycle to complete
			msgCommands = append([]tea.Cmd{tea.Tick(gdmPollFrequency, func(t time.Time) tea.Msg {
				return nil
			})}, msgCommands...)
		}
		commands = append(commands, msgCommands...)

	case gdmTestSendAuthDataWhenReady:
		doneMsg := gdmTestWaitForCommandsDone{seq: gdmTestSequentialMessages.Add(1)}
		m.wantMessages = append(m.wantMessages, doneMsg)
		commands = append(commands, sendEvent(gdmTestSendAuthDataWhenReadyFull{authData: msg.authData}))
		commands = append(commands, sendEvent(doneMsg))

	case gdmTestSendAuthDataWhenReadyFull:

		doneMsg := gdmTestWaitForCommandsDone{seq: gdmTestSequentialMessages.Add(1)}
		m.wantMessages = append(m.wantMessages, doneMsg)

		nextMsg := &gdmTestCommandsDone{commands: msg.commands, events: msg.events}
		if len(nextMsg.commands) > 0 || len(nextMsg.events) > 0 {
			seq := gdmTestSequentialMessages.Add(1)
			doneCommandsMsg := gdmTestWaitForCommandsDone{seq: seq}
			nextMsg.commands = append(nextMsg.commands, sendEvent(doneCommandsMsg))
			m.wantMessages = append(m.wantMessages, doneCommandsMsg)
		}

		go func() {
			m.gdmHandler.waitForAuthenticationStarted()
			if msg.authData != nil {
				m.gdmHandler.appendPollResultEvents(gdm_test.IsAuthenticatedEvent(msg.authData))
			}
			m.program.Send(tea.Sequence(tea.Tick(gdmPollFrequency, func(t time.Time) tea.Msg {
				return nil
			}), sendEvent(doneMsg), sendEvent(nextMsg))())
		}()

	case gdmTestWaitForCommandsDone:
		log.Debugf(context.TODO(), "Sequential messages done: %v", msg.seq)

	case stopAuthentication:
		m.gdmHandler.consumeAuthenticationStartedEvents()
	}

	m.maybeHandleWantMessageUnlocked(msg)
	return m, tea.Sequence(commands...)
}

func (m *gdmTestUIModel) filterFunc(model tea.Model, msg tea.Msg) tea.Msg {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := msg.(tea.QuitMsg); ok {
		// Quit is never sent to the Update func so we handle it earlier.
		m.maybeHandleWantMessageUnlocked(msg)
		if !m.programShouldQuit.Load() {
			return nil
		}
	}

	return MsgFilter(m.uiModel, msg)
}
