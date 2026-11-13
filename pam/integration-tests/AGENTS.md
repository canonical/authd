# Writing PAM integration tests

Read this guide before changing tests in this directory. These Go tests run real
PAM transactions against a test authd daemon and the example broker. They do not
need a cloud identity provider or the e2e VMs.

For changes to the PTY harness itself, also read
[`internal/testutils/ptytest/AGENTS.md`](../../internal/testutils/ptytest/AGENTS.md).
The VM tests have separate guidance in
[`e2e-tests/AGENTS.md`](../../e2e-tests/AGENTS.md).

## Start with the closest test

Use `native_test.go` for PAM conversation flows, `cli_test.go` for the terminal
UI, and `gdm_test.go` for GDM protocol flows. SSH tests live in `ssh_test.go`;
some version-specific cases run in LXD.

Reuse the setup in `helpers_test.go` and the interaction helpers in
`pty_helpers_test.go`. In particular:

- `preparePamRunnerTest` builds the PAM runner, exec module, and exec child in
  temporary directories. Do not install modules into the host's PAM stack.
- `sharedAuthd` supplies the test daemon and group-file paths. Use the existing
  per-test user-name helpers so parallel tests do not share user state.
- `startPAMRunner` starts a plain PTY session. `startCLIPAMRunner` also captures
  terminal snapshots for the TUI.
- Keep the runner's result and user assertions as well as the golden comparison.
  Correct screen output alone does not prove that authentication succeeded.

Use `t.Helper()` in helpers, `require` for assertions, and the existing cleanup
helpers. Register cleanup when acquiring a resource, including on failure paths.
Do not add shared mutable state to parallel tests.

## Synchronize with the reader

Wait for an observable condition instead of adding a fixed sleep. `WaitFor`
uses a regular expression and advances past each match. Use `regexp.QuoteMeta`
for literal text containing regexp characters.

A printed password label does not mean the reader has disabled terminal echo.
Before sending hidden input, synchronize with the terminal's echo state. This
also applies to cancellation keys sent at password prompts. Do not accept an
echoed password or key in a golden file to hide this race.

For non-empty, echoed input, use `sendEchoedLine` where the prompt format
supports it. It waits for the `> ` prompt, sends the text, waits for its echo,
and then sends Enter. Do not use it for hidden input or prompts without that
suffix.

For broker operations that wait for an external event, use the existing
completion-signal helpers. Do not guess when authentication should finish.

## Compare terminal output

Use `ptySanitizeOutput` for plain sessions and `ptySanitizeSnapshots` for TUI
sessions, then `golden.CheckOrUpdate`. The terminal emulator resolves cursor
movement and screen clearing; stripping ANSI escapes alone is not enough.

Snapshots capture successful matches and the final screen after exit. A match
in the middle of a redraw can capture a partial screen. Wait for the complete
state, using helpers such as `waitForRunnerResult`, before comparing it.

Update only the affected golden files. For example, from the repository root:

```bash
TESTS_UPDATE_GOLDEN=1 go test ./pam/integration-tests \
    -run '^TestNativeAuthenticate$/^Authenticate_user_successfully$' -count=1
```

Read the diff, then rerun without `TESTS_UPDATE_GOLDEN`. Normalize values that
are expected to vary, such as temporary paths, in the existing sanitizers.
Do not erase unexpected output or relax assertions to make a flaky test pass.

## Run and diagnose

Run commands from the repository root. Start with the affected subtest:

```bash
go test ./pam/integration-tests \
    -run '^TestNativeAuthenticate$/^Authenticate_user_successfully$' -count=1 -v
```

Do not use `-short` to validate these flows: it skips the integration tests.
The tests build their own binaries. See [`pam/README.md`](../README.md) for
build modes and the repository's
[`setup-go-tests` action](../../.github/actions/setup-go-tests/action.yaml) for
CI dependency setup.
SSH and LXD cases have additional requirements; check their setup and skip
conditions before treating a skipped run as validation.

On failure, read the PTY interaction history, process exit details, current
screen, and text since the last match. Check the saved daemon and PAM logs.
`AUTHD_TESTS_ARTIFACTS_PATH` selects the artifact directory;
`AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE=1` also preserves artifacts on success.

When diagnosing a timing failure, rerun the affected case with `-count` and,
where relevant, `-race` or `-asan`. Check that it actually ran. For Go changes,
also run `scripts/golangci-lint run` from the repository root.
