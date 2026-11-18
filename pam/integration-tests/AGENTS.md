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

Register cleanup when acquiring a resource, including on failure paths.
Do not add shared mutable state to parallel tests.

## Synchronize with the reader

Wait for an observable condition instead of adding a fixed sleep. `WaitFor`
uses a regular expression and advances past each match. Use `regexp.QuoteMeta`
for literal text containing regexp characters.

A printed password label does not mean the reader has disabled terminal echo.
For a native PAM prompt that disables echo, match the prompt, then call
`c.WaitForEchoOff(t)` before sending a password or a cancellation key. Do not
accept an echoed password or key in a golden file to hide this race.

For non-empty input at an echoed `> ` prompt, match the label first and let
`sendEchoedLine` wait for `> `. If snapshots are on, it discards the label frame.

For broker operations that wait for an external event, use the existing
completion-signal helpers. Do not guess when authentication should finish.

## Compare terminal output

Call `RequireExitCode` before comparing output. It checks the exit status,
drains the PTY, and captures the final frame when snapshots are enabled.

- For plain sessions, use `ptySanitizeOutput(t, c.RawOutput())`. `c.Output()`
  strips the escape sequences the terminal emulator needs.
- For sessions with snapshots enabled, use `ptySanitizeSnapshots(t, c)`. Wait
  for a complete screen. Use `waitForRunnerResult` to avoid matching just the
  action header.

Compare with `golden.CheckOrUpdate`. To update only the affected golden files,
prefix the targeted command below with `TESTS_UPDATE_GOLDEN=1`. Read the diff
and rerun without it. Normalize expected variation, such as temporary paths,
in the existing sanitizers. Do not erase unexpected output or relax assertions
to make a flaky test pass.

## Run and diagnose

Run commands from the repository root. Start with the affected subtest:

```bash
go test ./pam/integration-tests -count=1 -v \
    -run '^TestNativeAuthenticate$/^Authenticate_user_successfully_with_password_only_supported_method_in_polkit$'
```

Omit `-short`; it skips the example above.
The tests build their own binaries. See [`pam/README.md`](../README.md) for
build modes and the repository's
[`setup-go-tests` action](../../.github/actions/setup-go-tests/action.yaml) for
CI dependency setup.
SSH and LXD cases have additional requirements; check their setup and skip
conditions before treating a skipped run as validation. `RunTestInLXD` does
not forward `-race` or `-asan`. Pass the flag to `go test` inside the container.

On failure, read the PTY interaction history, process exit details, current
screen, and text since the last match. Check the saved daemon and PAM logs.
`AUTHD_TESTS_ARTIFACTS_PATH` selects the artifact directory;
`AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE=1` also preserves artifacts on success.
A shared daemon (the default with `-race`) then keeps its database in that
directory. Set `AUTHD_TESTS_USE_SHARED_AUTHD_INSTANCES=false` when repeating
tests that save artifacts on success so each run starts with a fresh database.

When diagnosing a timing failure, rerun the affected case with `-count` and,
where relevant, `-race` or `-asan`. Check that it actually ran.
