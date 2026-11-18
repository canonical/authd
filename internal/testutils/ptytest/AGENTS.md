# Maintaining the PTY test harness

Keep PAM-specific setup, prompts, and output normalization in the
[integration tests](../../../pam/integration-tests/AGENTS.md).

Watch for these pitfalls when changing the harness:

- `Signal` targets the command. `Close` signals the launcher's process group.
  Read `Start`, `Close`, and `launcher/main.go` before changing this handling.
- Command exit (`done`) and output completion (`copyDone`) are separate.
  Preserve the exit result for cleanup. `WaitForTimeout` checks drained output
  for a final match. `WaitForEchoOff` must report exit without waiting for it.
- In `WaitForEchoOff`, check for exit before accepting disabled echo. A
  timed-out wait must not leave polling work using the PTY during cleanup.
  `Close` must remain safe after failure and when called more than once.

Run `go test -race ./internal/testutils/ptytest` from the repository root.
Also run affected PAM integration cases when changing behavior they use.
