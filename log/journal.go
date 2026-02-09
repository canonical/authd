package log

import (
	"context"

	"github.com/coreos/go-systemd/v22/journal"
)

// InitJournalHandler makes the log package print to the journal if stderr is connected to the journal.
func InitJournalHandler(force bool) {
	if !force {
		isJournalStream, err := journal.StderrIsJournalStream()
		if err != nil {
			Warningf(context.Background(), "Error checking if stderr is connected to the journal: %v", err)
			return
		}
		if !isJournalStream {
			return
		}
	}

	SetHandler(func(_ context.Context, level Level, format string, args ...interface{}) {
		_ = journal.Print(mapPriority(level), format, args...)
	})
}

func mapPriority(level Level) journal.Priority {
	if level <= DebugLevel {
		return journal.PriDebug
	}
	if level <= InfoLevel {
		return journal.PriInfo
	}
	if level <= NoticeLevel {
		return journal.PriNotice
	}
	if level <= WarnLevel {
		return journal.PriWarning
	}
	if level <= ErrorLevel {
		return journal.PriErr
	}
	return journal.PriCrit
}
