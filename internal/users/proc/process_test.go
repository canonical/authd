package proc

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestAliveRetriesInterruptedPoll(t *testing.T) {
	original := poll
	t.Cleanup(func() { poll = original })
	poll = func(fds []unix.PollFd, timeout int) (int, error) {
		poll = original
		return 0, unix.EINTR
	}
	process, err := OpenProcess(os.Getpid())
	require.NoError(t, err)
	defer process.Close()
	alive, err := process.Alive()
	require.NoError(t, err)
	require.True(t, alive)
}

func TestPidfdTracksProcessExit(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	process, err := OpenProcess(cmd.Process.Pid)
	require.NoError(t, err)
	defer process.Close()
	cloned, err := process.Clone()
	require.NoError(t, err)
	defer cloned.Close()
	require.NoError(t, process.Close())
	alive, err := cloned.Alive()
	require.NoError(t, err)
	require.True(t, alive, "closing one handle must not close another kernel reference")
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	alive, err = cloned.Alive()
	require.NoError(t, err)
	require.False(t, alive)
	_, err = cloned.Clone()
	require.Error(t, err, "a reaped pidfd must not acquire a later process using that PID")
}

func TestProcessRejectsOrdinaryFile(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "not-a-pidfd")
	require.NoError(t, err)
	_, err = ProcessFromFile(file)
	require.Error(t, err)
}
