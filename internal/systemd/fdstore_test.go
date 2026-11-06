package systemd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPollRetriesInterruptionsWithinDeadline(t *testing.T) {
	original := poll
	t.Cleanup(func() { poll = original })
	synctest.Test(t, func(t *testing.T) {
		var waits []int
		poll = func(_ []unix.PollFd, timeout int) (int, error) {
			waits = append(waits, timeout)
			time.Sleep(time.Second)
			return 0, unix.EINTR
		}
		require.NoError(t, pollWithTimeout(nil, 3*time.Second))
		require.Equal(t, []int{3000, 2000, 1000}, waits, "interruptions must not reset the deadline")
	})
	poll = func(fds []unix.PollFd, _ int) (int, error) {
		poll = original
		return 0, unix.EINTR
	}
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	require.NoError(t, writer.Close())
	fds := []unix.PollFd{{Fd: int32(reader.Fd()), Events: unix.POLLHUP}} //nolint:gosec // Linux file descriptors fit in int32.
	require.NoError(t, pollWithTimeout(fds, time.Second))
	require.NotZero(t, fds[0].Revents&unix.POLLHUP)
}

func TestStoreWaitsForDescriptorAcceptance(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "partially_rejected"}[partial], func(t *testing.T) {
			t.Parallel()
			store, manager := notifyStore(t, partial)
			first, err := os.CreateTemp(t.TempDir(), "first")
			require.NoError(t, err)
			defer first.Close()
			second, err := os.CreateTemp(t.TempDir(), "second")
			require.NoError(t, err)
			defer second.Close()

			err = store.Put(StorePrefix+"owner", first, second)
			if partial {
				require.Error(t, err, "a barrier alone is not proof that both descriptors were accepted")
				require.Zero(t, manager.count())
				return
			}
			require.NoError(t, err)
			require.ErrorIs(t, store.Put(StorePrefix+"owner", first, second), ErrDescriptorNameExists)
			require.NoError(t, first.Close())
			require.NoError(t, second.Close())
			require.Equal(t, uint32(2), manager.count(), "systemd's references outlive the sender")
			require.NoError(t, store.Remove(StorePrefix+"owner"))
			require.Zero(t, manager.count())
		})
	}
}

func TestDescriptorCapacityCheckedBeforeSending(t *testing.T) {
	t.Parallel()
	store, manager := notifyStore(t, false)
	store.limit = 1
	first, err := os.CreateTemp(t.TempDir(), "first")
	require.NoError(t, err)
	defer first.Close()
	second, err := os.CreateTemp(t.TempDir(), "second")
	require.NoError(t, err)
	defer second.Close()
	require.Error(t, store.Put(StorePrefix+"owner", first, second))
	require.Zero(t, manager.count())
}

func TestPartialRemovalKeepsDescriptorCountInSync(t *testing.T) {
	t.Parallel()
	store, manager := notifyStore(t, false)
	first, err := os.CreateTemp(t.TempDir(), "first")
	require.NoError(t, err)
	defer first.Close()
	second, err := os.CreateTemp(t.TempDir(), "second")
	require.NoError(t, err)
	defer second.Close()
	require.NoError(t, store.Put(StorePrefix+"first", first))
	require.NoError(t, store.Put(StorePrefix+"second", second))
	send := store.send
	store.send = func(message string, files ...*os.File) error {
		if message == "FDSTOREREMOVE=1\nFDNAME="+StorePrefix+"second" {
			return errors.New("notification failed")
		}
		return send(message, files...)
	}
	require.ErrorContains(t, store.Remove(StorePrefix+"first", StorePrefix+"second"), "notification failed")
	require.Equal(t, uint32(1), manager.count())
	require.NoError(t, store.Put(StorePrefix+"third", first), "a partial removal must not block new reservations")
	store.send = send
	require.NoError(t, store.Remove(StorePrefix+"first", StorePrefix+"second", StorePrefix+"third"))
	require.Zero(t, manager.count())
}

func TestSocketDescriptorStoreSurvivesDaemonExit(t *testing.T) {
	t.Parallel()
	store := Z_ForTests_NewDescriptorStore(t)
	socket := filepath.Join(t.TempDir(), "store.socket")
	store.Serve(t, socket)
	first := Z_ForTests_DescriptorStoreClient(socket)
	file, err := os.CreateTemp(t.TempDir(), "metadata")
	require.NoError(t, err)
	require.NoError(t, first.Put(StorePrefix+"owner", file))
	require.ErrorIs(t, first.Put(StorePrefix+"owner", file), ErrDescriptorNameExists)
	require.NoError(t, file.Close())
	require.NoError(t, first.Close())

	second := Z_ForTests_DescriptorStoreClient(socket)
	files := second.Load()
	require.Len(t, files[StorePrefix+"owner"], 1)
	require.NoError(t, files[StorePrefix+"owner"][0].Close())
	store.mu.Lock()
	store.FailRemove = true
	store.mu.Unlock()
	require.ErrorContains(t, second.Remove(StorePrefix+"owner"), "removal failed")
	store.mu.Lock()
	store.FailRemove = false
	store.mu.Unlock()
	require.NoError(t, second.Remove(StorePrefix+"owner"))
	require.Empty(t, second.Load())
}

func TestActivationSeparatesStoredDescriptors(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "authd.sock"))
	require.NoError(t, err)
	defer listener.Close()
	unixListener, ok := listener.(*net.UnixListener)
	require.True(t, ok)
	socket, err := unixListener.File()
	require.NoError(t, err)
	defer socket.Close()
	metadata, err := os.CreateTemp(t.TempDir(), "metadata")
	require.NoError(t, err)
	defer metadata.Close()
	listeners, err := listenersFromFiles(map[string][]*os.File{
		"authd.socket":             {socket},
		StorePrefix + "generation": {metadata},
	})
	require.NoError(t, err)
	require.Len(t, listeners, 1)
	listeners[0].Close()
	_, err = metadata.Stat()
	require.NoError(t, err, "socket activation must not close stored descriptors")
}

type notifyManager struct {
	mu      sync.Mutex
	files   map[string][]*os.File
	partial bool
}

func (m *notifyManager) count() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count uint32
	for _, files := range m.files {
		count += uint32(len(files)) //nolint:gosec // Tests store at most two descriptors.
	}
	return count
}

func notifyStore(t *testing.T, partial bool) (*descriptorStore, *notifyManager) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	require.NoError(t, err)
	manager := &notifyManager{files: make(map[string][]*os.File), partial: partial}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			data := make([]byte, 4096)
			rights := make([]byte, unix.CmsgSpace(8*4))
			n, oobn, _, _, err := listener.ReadMsgUnix(data, rights)
			if err != nil {
				return
			}
			messages, err := unix.ParseSocketControlMessage(rights[:oobn])
			if err != nil {
				return
			}
			var files []*os.File
			for _, message := range messages {
				fds, err := unix.ParseUnixRights(&message)
				if err != nil {
					return
				}
				for _, fd := range fds {
					files = append(files, os.NewFile(uintptr(fd), "received"))
				}
			}
			fields := make(map[string]string)
			for _, field := range strings.Split(string(data[:n]), "\n") {
				key, value, _ := strings.Cut(field, "=")
				fields[key] = value
			}
			manager.mu.Lock()
			switch {
			case fields["FDSTORE"] == "1":
				if manager.partial && len(files) > 1 {
					files[1].Close()
					files = files[:1]
				}
				manager.files[fields["FDNAME"]] = files
			case fields["FDSTOREREMOVE"] == "1":
				for _, file := range manager.files[fields["FDNAME"]] {
					file.Close()
				}
				delete(manager.files, fields["FDNAME"])
			case fields["BARRIER"] == "1":
				for _, file := range files {
					file.Close()
				}
			}
			manager.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
		for _, files := range manager.files {
			for _, file := range files {
				file.Close()
			}
		}
	})
	store := &descriptorStore{
		socket: socket, limit: 8193, sizes: make(map[string]int),
		count: func(context.Context) (uint32, error) { return manager.count(), nil },
		close: func() {},
	}
	store.send = store.notify
	return store, manager
}
