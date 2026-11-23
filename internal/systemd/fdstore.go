package systemd

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"golang.org/x/sys/unix"
)

// DescriptorStore preserves descriptors outside the authd process.
type DescriptorStore interface {
	Load() map[string][]*os.File
	Put(name string, files ...*os.File) error
	Remove(names ...string) error
	Close() error
}

// ErrDescriptorNameExists means Put rejected a name before changing the store.
var ErrDescriptorNameExists = errors.New("descriptor store name already exists")

type descriptorStore struct {
	socket string
	limit  int
	files  map[string][]*os.File
	sizes  map[string]int
	count  func(context.Context) (uint32, error)
	send   func(string, ...*os.File) error
	close  func()
}

// NewDescriptorStore returns nil when authd is not running under a configured
// systemd service. Such instances must not expose first-time SSH reservations.
func NewDescriptorStore() (DescriptorStore, error) {
	socket := os.Getenv("NOTIFY_SOCKET")
	limit, err := strconv.Atoi(os.Getenv("FDSTORE"))
	if socket == "" || os.Getenv("FDSTORE") == "" {
		return nil, nil
	}
	if err != nil || limit <= 0 {
		return nil, errors.New("invalid systemd descriptor store limit")
	}
	conn, err := dbus.NewSystemConnectionContext(context.Background())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Linux process IDs fit in uint32.
	unit, err := conn.GetUnitNameByPID(ctx, uint32(os.Getpid())) //nolint:gosec // Linux PIDs are positive signed int32 values.
	if err != nil {
		conn.Close()
		return nil, err
	}
	s := &descriptorStore{
		socket: socket, limit: limit, files: storedFiles(), sizes: make(map[string]int),
		close: conn.Close,
		count: func(ctx context.Context) (uint32, error) {
			property, err := conn.GetServicePropertyContext(ctx, unit, "NFileDescriptorStore")
			if err != nil {
				return 0, err
			}
			count, ok := property.Value.Value().(uint32)
			if !ok {
				return 0, errors.New("invalid systemd descriptor store count")
			}
			return count, nil
		},
	}
	s.send = s.notify
	for name, files := range s.files {
		s.sizes[name] = len(files)
	}
	if err := s.checkCount(s.expectedCount()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *descriptorStore) Load() map[string][]*os.File {
	files := s.files
	s.files = nil
	return files
}

func (s *descriptorStore) expectedCount() int {
	count := 0
	for _, size := range s.sizes {
		count += size
	}
	return count
}

func (s *descriptorStore) Put(name string, files ...*os.File) error {
	if !strings.HasPrefix(name, StorePrefix) || strings.ContainsAny(name, ":\n") || len(name) > 255 {
		return errors.New("invalid descriptor store name")
	}
	if _, exists := s.sizes[name]; exists {
		return ErrDescriptorNameExists
	}
	before := s.expectedCount()
	if before+len(files) > s.limit {
		return errors.New("systemd descriptor store is full")
	}
	if err := s.checkCount(before); err != nil {
		return err
	}
	if err := s.send("FDSTORE=1\nFDPOLL=0\nFDNAME="+name, files...); err != nil {
		return err
	}
	err := s.barrier()
	if err == nil {
		err = s.checkCount(before + len(files))
	}
	if err != nil {
		// Notifications can be accepted only in part. Never expose an ID until
		// the count confirms that both its metadata and pidfd were retained.
		_ = s.send("FDSTOREREMOVE=1\nFDNAME=" + name)
		_ = s.barrier()
		return err
	}
	s.sizes[name] = len(files)
	return nil
}

func (s *descriptorStore) Remove(names ...string) error {
	var notified []string
	var notifyErr error
	size := 0
	for _, name := range names {
		count, exists := s.sizes[name]
		if !exists {
			continue
		}
		if err := s.send("FDSTOREREMOVE=1\nFDNAME=" + name); err != nil {
			notifyErr = err
			break
		}
		notified = append(notified, name)
		size += count
	}
	if size == 0 {
		return notifyErr
	}
	if err := s.barrier(); err != nil {
		return errors.Join(notifyErr, err)
	}
	if err := s.checkCount(s.expectedCount() - size); err != nil {
		return errors.Join(notifyErr, err)
	}
	for _, name := range notified {
		delete(s.sizes, name)
	}
	return notifyErr
}

func (s *descriptorStore) checkCount(expected int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actual, err := s.count(ctx)
	if err != nil {
		return err
	}
	if int64(actual) != int64(expected) {
		return fmt.Errorf("systemd retained %d descriptors, expected %d", actual, expected)
	}
	return nil
}

func (s *descriptorStore) notify(message string, files ...*os.File) error {
	address := s.socket
	if strings.HasPrefix(address, "@") {
		address = "\x00" + address[1:]
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{
		Name: "@authd-notify-" + rand.Text(), Net: "unixgram",
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
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
	_, _, err = conn.WriteMsgUnix([]byte(message), rights, &net.UnixAddr{Name: address, Net: "unixgram"})
	return err
}

func (s *descriptorStore) barrier() error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := s.send("BARRIER=1", writer); err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	fds := []unix.PollFd{{Fd: int32(reader.Fd()), Events: unix.POLLHUP}} //nolint:gosec // Linux file descriptors fit in int32.
	if err := pollWithTimeout(fds, 5*time.Second); err != nil {
		return err
	}
	if fds[0].Revents&unix.POLLHUP == 0 {
		return errors.New("systemd descriptor store acknowledgement timed out")
	}
	return nil
}

var poll = unix.Poll

func pollWithTimeout(fds []unix.PollFd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		remaining := max(int(time.Until(deadline).Milliseconds()), 0)
		_, err := poll(fds, remaining)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
		if !time.Now().Before(deadline) {
			return nil
		}
	}
}

func (s *descriptorStore) Close() error {
	for _, files := range s.files {
		for _, file := range files {
			file.Close()
		}
	}
	s.close()
	return nil
}
