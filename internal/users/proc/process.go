package proc

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// Process holds a kernel reference to one process, independent of PID reuse.
type Process struct {
	mu   sync.Mutex
	file *os.File
}

var poll = unix.Poll

// OpenProcess opens a pidfd for a local process. RPC callers must instead use
// the pidfd supplied by SO_PEERPIDFD.
func OpenProcess(pid int) (*Process, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}
	return ProcessFromFile(os.NewFile(uintptr(fd), "pidfd"))
}

// ProcessFromFile takes ownership of a pidfd, including on error.
func ProcessFromFile(file *os.File) (*Process, error) {
	p := &Process{file: file}
	if _, err := p.PID(); err != nil {
		file.Close()
		return nil, err
	}
	return p, nil
}

// PID returns the process ID, or -1 if the process has been reaped.
func (p *Process) PID() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return 0, errors.New("closed pidfd")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", p.file.Fd()))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "Pid:"); ok {
			return strconv.Atoi(strings.TrimSpace(value))
		}
	}
	return 0, errors.New("file descriptor is not a pidfd")
}

// Alive checks the kernel reference, not the numeric PID.
func (p *Process) Alive() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return false, errors.New("closed pidfd")
	}
	fd := p.file.Fd()
	if fd > math.MaxInt32 {
		return false, errors.New("invalid pidfd")
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		_, err := poll(fds, 0)
		if !errors.Is(err, unix.EINTR) {
			if err != nil {
				return false, err
			}
			break
		}
	}
	if fds[0].Revents&unix.POLLNVAL != 0 {
		return false, errors.New("invalid pidfd")
	}
	return fds[0].Revents == 0, nil
}

// Clone opens a separate pidfd for the same live process. A separate open file
// description prevents systemd from deduplicating owners of different users.
func (p *Process) Clone() (*Process, error) {
	pid, err := p.PID()
	if err != nil {
		return nil, err
	}
	if pid <= 0 {
		return nil, errors.New("process has exited")
	}
	cloned, err := OpenProcess(pid)
	if err != nil {
		return nil, err
	}
	alive, err := p.Alive()
	if err != nil || !alive {
		cloned.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("process has exited")
	}
	return cloned, nil
}

// File returns the pidfd. The caller must not close it.
func (p *Process) File() *os.File {
	return p.file
}

// Close releases this process reference.
func (p *Process) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}
