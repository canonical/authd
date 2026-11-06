package systemd

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/canonical/authd/internal/testsdetection"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type storeMessage struct {
	Action        string
	Names         []string
	Error         string
	DuplicateName bool
}

func sendStoreMessage(conn *net.UnixConn, message storeMessage, files ...*os.File) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var fds []int
	for _, file := range files {
		fds = append(fds, int(file.Fd()))
	}
	var rights []byte
	if len(fds) > 0 {
		rights = unix.UnixRights(fds...)
	}
	_, _, err = conn.WriteMsgUnix(data, rights, nil)
	return err
}

func receiveStoreMessage(conn *net.UnixConn) (storeMessage, []*os.File, error) {
	data := make([]byte, 4096)
	rights := make([]byte, unix.CmsgSpace(8*4))
	n, oobn, flags, _, err := conn.ReadMsgUnix(data, rights)
	if err != nil {
		return storeMessage{}, nil, err
	}
	messages, err := unix.ParseSocketControlMessage(rights[:oobn])
	if err != nil {
		return storeMessage{}, nil, err
	}
	var files []*os.File
	for _, message := range messages {
		fds, err := unix.ParseUnixRights(&message)
		if err != nil {
			return storeMessage{}, files, err
		}
		for _, fd := range fds {
			unix.CloseOnExec(fd)
			files = append(files, os.NewFile(uintptr(fd), "test-store"))
		}
	}
	if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		return storeMessage{}, files, errors.New("truncated descriptor store message")
	}
	var message storeMessage
	err = json.Unmarshal(data[:n], &message)
	if err == nil && message.Error != "" {
		err = errors.New(message.Error)
		if message.DuplicateName {
			err = ErrDescriptorNameExists
		}
	}
	return message, files, err
}

func closeStoreFiles(files []*os.File) {
	for _, file := range files {
		file.Close()
	}
}

// Serve retains subprocess descriptors until the test ends, including across
// daemon restarts. The socket must be in a private test directory.
func (s *TestDescriptorStore) Serve(tb testing.TB, socket string) {
	tb.Helper()
	testsdetection.MustBeTesting()
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: socket, Net: "unixpacket"})
	require.NoError(tb, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			s.serveConnection(conn)
			conn.Close()
		}
	}()
	tb.Cleanup(func() {
		listener.Close()
		<-done
	})
}

func (s *TestDescriptorStore) serveConnection(conn *net.UnixConn) {
	message, files, err := receiveStoreMessage(conn)
	defer closeStoreFiles(files)
	if err == nil {
		switch message.Action {
		case "load":
			groups := s.Load()
			for name, files := range groups {
				if err == nil {
					err = sendStoreMessage(conn, storeMessage{Names: []string{name}}, files...)
				}
				closeStoreFiles(files)
			}
		case "put":
			if len(message.Names) != 1 {
				err = errors.New("expected one descriptor group")
			} else {
				err = s.Put(message.Names[0], files...)
			}
		case "remove":
			err = s.Remove(message.Names...)
		default:
			err = errors.New("unknown descriptor store action")
		}
	}
	reply := storeMessage{Action: "done"}
	if err != nil {
		reply.Error = err.Error()
		reply.DuplicateName = errors.Is(err, ErrDescriptorNameExists)
	}
	_ = sendStoreMessage(conn, reply)
}

type socketTestStore struct {
	socket string
}

// Z_ForTests_DescriptorStoreClient connects integration-test daemons to the
// descriptors retained by their parent test process.
//
//nolint:revive // Test helper naming convention.
func Z_ForTests_DescriptorStoreClient(socket string) DescriptorStore {
	testsdetection.MustBeTesting()
	return &socketTestStore{socket: socket}
}

func (s *socketTestStore) request(message storeMessage, files ...*os.File) (map[string][]*os.File, error) {
	testsdetection.MustBeTesting()
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: s.socket, Net: "unixpacket"})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	if err := sendStoreMessage(conn, message, files...); err != nil {
		return nil, err
	}
	groups := make(map[string][]*os.File)
	for {
		reply, files, err := receiveStoreMessage(conn)
		if err == nil && reply.Action == "done" {
			closeStoreFiles(files)
			return groups, nil
		}
		if err == nil && len(reply.Names) != 1 {
			err = errors.New("invalid descriptor store reply")
		}
		if err != nil {
			closeStoreFiles(files)
			for _, files := range groups {
				closeStoreFiles(files)
			}
			return nil, err
		}
		groups[reply.Names[0]] = files
	}
}

func (s *socketTestStore) Load() map[string][]*os.File {
	groups, err := s.request(storeMessage{Action: "load"})
	if err != nil {
		panic(err)
	}
	return groups
}

func (s *socketTestStore) Put(name string, files ...*os.File) error {
	_, err := s.request(storeMessage{Action: "put", Names: []string{name}}, files...)
	return err
}

func (s *socketTestStore) Remove(names ...string) error {
	// Keep each packet bounded even when pruning thousands of owners.
	for _, name := range names {
		if _, err := s.request(storeMessage{Action: "remove", Names: []string{name}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *socketTestStore) Close() error {
	return nil
}
