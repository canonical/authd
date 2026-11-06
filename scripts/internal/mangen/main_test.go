package main

import (
	"testing"

	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
)

// sampleHelp has the same sections as the script's help text. It also has a
// backslash in the description, which must be escaped in roff.
const sampleHelp = `demo-tool - apply demo settings

Usage: demo-tool --name NAME [--apply] [--path PATH] TOKEN

Read one token file. Without --apply, print the plan only.

NAME is the value from the [demo] section. Paths like C:\tmp keep their backslash.

Environment: TOOL_CMD sets the command to run. The default is tool.

Exit status: 0 on success. 1 on any error.
`

func TestRender(t *testing.T) {
	t.Parallel()

	got, err := render(sampleHelp)
	require.NoError(t, err)

	golden.CheckOrUpdate(t, got)
}

func TestRenderEscapesRoffCharacters(t *testing.T) {
	t.Parallel()

	help := `demo-tool - apply demo settings

Usage: demo-tool [--apply] ARG

.Starts with a dot, so it needs an escape. Paths like C:\tmp are escaped too.

Environment: none.

Exit status: 0.
`

	got, err := render(help)
	require.NoError(t, err)

	require.Contains(t, got, `\fBdemo\-tool\fP [\-\-apply] \fIARG\fP`, "the usage line is formatted")
	require.Contains(t, got, `\&.Starts with a dot`, "a line that starts with a dot is escaped")
	require.Contains(t, got, `C:\etmp`, "a backslash is escaped")
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		help    string
		wantErr string
	}{
		"Fails_on_empty_help": {
			help:    "",
			wantErr: "help text is empty",
		},
		"Fails_when_the_summary_is_not_one_line": {
			help:    "Not a summary\n\nUsage: demo-tool\n\nEnvironment: none.\n\nExit status: 0.\n",
			wantErr: "one-line summary",
		},
		"Fails_when_the_summary_has_a_name_with_spaces": {
			help:    "demo tool - apply\n\nUsage: demo\n\nEnvironment: none.\n\nExit status: 0.\n",
			wantErr: "one-line summary",
		},
		"Fails_when_usage_is_missing": {
			help:    "demo-tool - apply\n\nEnvironment: none.\n\nExit status: 0.\n",
			wantErr: `no "Usage:" paragraph`,
		},
		"Fails_when_environment_is_missing": {
			help:    "demo-tool - apply\n\nUsage: demo-tool\n\nExit status: 0.\n",
			wantErr: `no "Environment:" paragraph`,
		},
		"Fails_when_exit_status_is_missing": {
			help:    "demo-tool - apply\n\nUsage: demo-tool\n\nEnvironment: none.\n",
			wantErr: `no "Exit status:" paragraph`,
		},
		"Fails_when_a_section_is_repeated": {
			help:    "demo-tool - apply\n\nUsage: demo-tool\n\nUsage: demo-tool\n\nEnvironment: none.\n\nExit status: 0.\n",
			wantErr: `more than one "Usage:" paragraph`,
		},
		"Fails_when_usage_does_not_start_with_the_command_name": {
			help:    "demo-tool - apply\n\nUsage: other-tool\n\nEnvironment: none.\n\nExit status: 0.\n",
			wantErr: "must start with the command name",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := render(tc.help)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRenderScriptHelp(t *testing.T) {
	t.Parallel()

	help, err := scriptHelp("../../apply-entra-unix-ids")
	require.NoError(t, err)

	got, err := render(help)
	require.NoError(t, err)

	require.Contains(t, got, `.SH "EXIT STATUS"`)
	require.Contains(t, got, `\-\-uid\-attribute`)
	require.Contains(t, got, `\fBapply\-entra\-unix\-ids\fP`)
}
