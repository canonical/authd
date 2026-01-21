package localentries

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
)

func parseLocalPasswdFile(passwdFile string) (entries []types.UserEntry, err error) {
	defer decorate.OnError(&err, "could not parse local passwd file %s", passwdFile)

	log.Debugf(context.Background(), "Parsing local passwd file: %s", passwdFile)

	f, err := os.Open(passwdFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// The format of the local passwd file is:
	// username:password:uid:gid:gecos:home:shell
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue // Skip empty lines and comments
		}

		fields := strings.SplitN(line, ":", 7)
		if len(fields) < 7 {
			log.Warningf(context.Background(), "Skipping invalid entry in %s (invalid format): %s", passwdFile, line)
			continue
		}

		username, uidValue, gidValue, gecos, home, shell :=
			fields[0], fields[2], fields[3], fields[4], fields[5], fields[6]

		uid, err := strconv.ParseUint(uidValue, 10, 32)
		if err != nil {
			log.Warningf(context.Background(), "Skipping invalid entry in %s (invalid UID): %s", passwdFile, line)
			continue
		}

		gid, err := strconv.ParseUint(gidValue, 10, 32)
		if err != nil {
			log.Warningf(context.Background(), "Skipping invalid entry in %s (invalid GID): %s", passwdFile, line)
			continue
		}

		entry := types.UserEntry{
			Name:  username,
			UID:   uint32(uid),
			GID:   uint32(gid),
			Gecos: gecos,
			Dir:   home,
			Shell: shell,
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}
