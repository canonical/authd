# Maintaining the PTY test harness

Keep PAM-specific setup, prompts, and output normalization in the
[integration tests](../../../pam/integration-tests/AGENTS.md).

Watch for these pitfalls when changing the harness:

- The command PID and launcher PID are different. Read `Start`, `Close`, and
  `launcher/main.go` before changing signal or process handling.
- Command exit (`done`) and output completion (`copyDone`) are separate.
  Descendants can keep the PTY open after exit. Do not wait for output EOF to
  report early-exit failures, and preserve the exit result for cleanup.
- A password label can appear before echo is disabled. Synchronize hidden input
  with terminal state, not a delay or label match. Check exit before accepting
  terminal state left behind by a dead command.
- A timed-out wait must not leave polling work using the PTY during cleanup.
  `Close` must remain safe after failure and when called more than once.

Run from the repository root:

```bash
go test -race ./internal/testutils/ptytest
scripts/golangci-lint run
```

Also run affected PAM integration cases when changing behavior they use.
