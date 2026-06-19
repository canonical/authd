// Package proc contains utilities for checking processes via /proc.
package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/canonical/authd/log"
)

// ErrUserBusy is returned by CheckUserBusy if the user is currently used by a process.
var ErrUserBusy = errors.New("user is currently used by process")

// CheckUserBusy checks if a user is currently running any processes.
//
// It is a re-implementation of this user_busy_processes() function:
// https://github.com/shadow-maint/shadow/blob/e78742e553c12222b40b13224d3b0fafaceae791/lib/user_busy.c#L164-L272
//
// It returns ErrUserBusy if the user has active processes, nil otherwise.
func CheckUserBusy(name string, uid uint32) error {
	log.Debugf(context.Background(), "Checking if user %s (uid %d) is busy", name, uid)
	var rootStat syscall.Stat_t
	if err := syscall.Stat("/", &rootStat); err != nil {
		return fmt.Errorf("stat (\"/\"): %w", err)
	}

	procDir, err := os.Open("/proc")
	if err != nil {
		return fmt.Errorf("opendir /proc: %w", err)
	}
	defer func() {
		_ = procDir.Close()
	}()

	entries, err := procDir.Readdir(-1)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		entryName := entry.Name()

		/*
		 * Ingo Molnar's patch introducing NPTL for 2.4 hides
		 * threads in the /proc directory by prepending a period.
		 * This patch is applied by default in some RedHat
		 * kernels.
		 */
		if entryName == "." || entryName == ".." {
			continue
		}
		entryName = strings.TrimPrefix(entryName, ".")

		/* Check if this is a valid PID */
		pid, err := strconv.Atoi(entryName)
		if err != nil {
			continue
		}

		/* Check if the process is in our chroot */
		rootPath := fmt.Sprintf("/proc/%d/root", pid)
		var procRootStat syscall.Stat_t
		if err := syscall.Stat(rootPath, &procRootStat); err != nil {
			continue
		}
		if rootStat.Dev != procRootStat.Dev || rootStat.Ino != procRootStat.Ino {
			continue
		}

		if checkStatus(name, entryName, uid) {
			return fmt.Errorf("%w %d", ErrUserBusy, pid)
		}

		taskPath := fmt.Sprintf("/proc/%d/task", pid)
		taskDir, err := os.Open(taskPath)
		if err != nil {
			log.Debugf(context.Background(), "Skipping invalid task path %q: %v", taskPath, err)
			continue
		}

		taskEntries, err := taskDir.Readdir(-1)
		_ = taskDir.Close()
		if err != nil {
			log.Debugf(context.Background(), "Skipping invalid task directory %q: %v", taskPath, err)
			continue
		}

		for _, taskEntry := range taskEntries {
			tid, err := strconv.Atoi(taskEntry.Name())
			if err != nil || tid == pid {
				continue
			}

			taskStatusPath := filepath.Join(strconv.Itoa(pid), "task", taskEntry.Name())
			if checkStatus(name, taskStatusPath, uid) {
				return fmt.Errorf("%w %d", ErrUserBusy, pid)
			}
		}
	}

	return nil
}

func differentNamespace(sname string) bool {
	path := filepath.Join("/proc", sname, "ns", "user")

	dest1, err := os.Readlink(path)
	if err != nil {
		return false
	}

	dest2, err := os.Readlink("/proc/self/ns/user")
	if err != nil {
		return false
	}

	return dest1 != dest2
}

func checkStatus(username, sname string, uid uint32) bool {
	statusPath := filepath.Join("/proc", sname, "status")
	f, err := os.Open(statusPath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "Uid:\t"); ok {
			fields := strings.Fields(rest)
			if len(fields) < 3 {
				return false
			}
			var uids [3]uint32
			for i := 0; i < 3; i++ {
				uid64, err := strconv.ParseUint(fields[i], 10, 32)
				if err != nil {
					return false
				}
				uids[i] = uint32(uid64)
			}
			realUID, effectiveUID, savedUID := uids[0], uids[1], uids[2]

			if realUID == uid || effectiveUID == uid || savedUID == uid {
				return true
			}

			// Check sub-UIDs only if in different namespace
			if differentNamespace(sname) &&
				(checkSubUID(username, realUID) ||
					checkSubUID(username, effectiveUID) ||
					checkSubUID(username, savedUID)) {
				return true
			}

			return false
		}
	}
	if err := scanner.Err(); err != nil {
		return false
	}

	return false
}

func checkSubUID(username string, nsUID uint32) bool {
	f, err := os.Open("/etc/subuid")
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ":")
		if len(fields) != 3 {
			continue
		}
		if fields[0] != username {
			continue
		}

		start, err1 := strconv.ParseUint(fields[1], 10, 32)
		count, err2 := strconv.ParseUint(fields[2], 10, 32)
		if err1 != nil || err2 != nil {
			log.Debugf(context.Background(), "Invalid subuid entry %q: %v", line, err)
			continue
		}

		if nsUID >= uint32(start) && nsUID < uint32(start)+uint32(count) {
			return true
		}
	}
	if err := scanner.Err(); err != nil {
		return false
	}

	return false
}
