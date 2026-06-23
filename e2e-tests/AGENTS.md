# Writing authd e2e tests (guide for AI agents)

This is the entry point for creating end-to-end tests in this directory. Read it
before writing or editing any `.robot` or `.resource` file.

These tests are **real** end-to-end tests: they boot an Ubuntu VM, install authd
and a real broker snap, log in through GDM / SSH / `machinectl login`,
authenticate against a **real identity provider** (device-code flow driven by a
headless browser), and assert on the system journal and the on-screen text. They
are written in [Robot Framework](https://robotframework.org/) and run via
[YARF](https://github.com/canonical/yarf), which drives the VM's display over
VNC and matches screen text with OCR.

See `TESTING.md` for the full environment-setup and provisioning steps. This
guide is specifically about *authoring* tests and the feedback loops available
to you.

## The core difficulty

You cannot see the screen while writing a test. Most assertions go through OCR
(`Match Text` / `Find Text`), and OCR is non-deterministic: it reads spaces that
aren't there, mis-cases letters, and is sensitive to confidence thresholds. A
match that looks obviously correct can fail at runtime. The two habits below
exist to close that gap:

1. Prefer assertions that don't depend on OCR at all.
2. When OCR is unavoidable, **probe it live in the interactive console** before
   committing the match (see "Developing OCR matches").

## Choosing how to assert — in priority order

1. **`SSH.Execute` / `getent` / journal.** If the thing you're checking is
   observable over SSH or in the journal, assert it there — it's deterministic
   and fast. The suite already does this where it can: see `Check Home Directory`
   and `Check If Owner Was Registered` in `resources/broker.resource`, which use
   `SSH.Execute` and `getent` instead of reading the screen.
2. **OCR (`Match Text` / `Find Text` / `Read Text`).** Use this only when the
   behavior is genuinely only observable on screen (GDM, the PAM CLI/TUI inside
   a `machinectl` session, terminal output you can't reach over SSH). Follow the
   OCR hygiene rules below.

Note that SSH is often *not* possible — e.g. anything happening inside the GDM
greeter or a `machinectl login` session before the user's shell exists. Don't
force an SSH assertion where it doesn't fit; use OCR and make it robust.

## Don't sleep — poll instead

Avoid `Builtin.Sleep` to wait for asynchronous work to finish (e.g. authd's
post-login device registration and MS Graph group fetch before follow-on
provisioning assertions). A fixed sleep is flaky in both directions: it fails
when the work takes longer than expected, and it wastes time when the work
finishes sooner.

Instead, poll for the actual completion condition with
`Wait Until Keyword Succeeds`, wrapping a deterministic check (`SSH.Execute` /
`getent` / journal per the priority order above). For example, rather than
sleeping after login and hoping the owner is registered, retry the registration
check until it passes:

```robotframework
# Bad: hope 5s is enough for post-login work to finish.
Builtin.Sleep    5
Check If Owner Was Registered

# Good: poll until the post-login work is actually observable.
Wait Until Keyword Succeeds    30s    1s    Check If Owner Was Registered
```

This is just as deterministic, returns as soon as the condition is met, and
tolerates a slow VM. A bare `Match Text` already polls (it retries until its
timeout), so it needs no extra wrapping; reserve `Wait Until Keyword Succeeds`
for the non-OCR checks that don't retry on their own. Only fall back to a fixed
sleep when there is genuinely no observable signal to poll on.

## The authoring loop

1. **Model on the closest existing test.** Find the `.robot` file under `tests/`
   that most resembles what you're adding and follow its structure (setup,
   teardown, keyword usage).
2. **Reuse keywords; don't reinvent them.** Before writing a keyword that
   might already exist, check these sources:
   - **authd shared resources** in `resources/utils.resource`,
     `resources/authd.resource`, and `resources/broker.resource`.
   - **YARF built-ins** (`Hid.*`, `Match Text`, `Find Text`, `Read Text`,
     etc.) — see the [YARF keyword reference](https://canonical-yarf.readthedocs-hosted.com/en/latest/reference/)
     or browse `.yarf/docs/reference/rf_libraries/`.
   - **Robot Framework standard libraries** (`BuiltIn`, `Collections`,
     `OperatingSystem`, `String`, `Process`, `SSH`, …) — see
     https://robotframework.org/robotframework/; if the Context7 MCP tool
     is available, you can also use that (library ID 
     `/robotframework/robotframework`).

   Compose from these instead of writing new low-level keystroke/OCR
   sequences. High-value keywords and idioms:
   - `Log In`, `Wait Until Desktop Ready`, `Open Terminal`,
     `Run Command In Terminal` — GDM/desktop/terminal lifecycle.
   - `Try machinectl login Prompt` — retries the `machinectl login` prompt,
     which is flaky; always use it rather than typing `machinectl login`
     directly.
   - `Log In With Remote User Through CLI/SSH/GDM: QR Code` /
     `: Local Password` — the full broker login flows.
   - **Exiting a `machinectl` session:** press `Ctrl+]` three times in quick
     succession (see `Log Out From Terminal Session`). A single press does not
     exit.
   - **The `base64 cmd-finished` trick** (see `Run Command In Terminal`): to
     detect a command finished without OCR-matching the command echo itself,
     append `&& echo Y21kLWZpbmlzaGVkCg== | base64 -d` and `Match Text
     cmd-finished`. Reuse this pattern for new terminal commands.
3. **Develop any new OCR matches in the interactive console** (next section).
4. **Run the test and read the live trace** (see "Running and reading results").
5. Iterate.

## Developing OCR matches (the interactive console)

`yarf-console.sh` launches YARF's interactive Robot Framework REPL connected to
the live VM over VNC. This is the tool for turning a blind OCR guess into a
verified match: drive the VM to the screen you care about, ask the OCR what it
actually reads, and refine until it matches reliably.

```bash
# Restores the broker snapshot and drops you into the REPL.
./yarf-console.sh --broker authd-google --release noble
```

Inside the console, load the authd keywords (the console starts with only YARF's
own libraries imported):

```robotframework
Import Resource    ${CURDIR}/resources/authd.resource
Import Resource    ${CURDIR}/resources/broker.resource
```

Then probe. `Read Text` dumps everything the OCR currently sees; `Find Text`
returns the matches (empty list = no match) without failing the step, which is
ideal for experimenting:

```robotframework
Read Text                                  # what does OCR see right now?
${m}=    Find Text    Select your provider # does this exact string match?
Log To Console    ${m}
${m}=    Find Text    regex:2\\. .*Google  # try a regex if the literal is flaky
```

Drive the screen with the same keywords the tests use (`Hid.Type String`,
`Hid.Keys Combo`, `Try machinectl login Prompt`, etc.) to reach each state, and
settle on the match *there* before pasting it into your `.robot`/`.resource`
file. The console history is saved to `rfdebug_history.log` in the output dir.

For a region that OCR keeps misreading, the console also exposes `Grab
Templates` and a region-of-interest selector to crop and scope matches.

## OCR hygiene rules

These come straight from patterns the existing suite relies on — reproduce them:

- **Strip spaces and normalize case** when matching machine-generated codes. The
  device user code handling in `Continue Log In With Remote User: Authenticate
  In External Browser` removes OCR-inserted spaces and upper-cases the result,
  because OCR adds phantom spaces and mis-cases characters.
- **Prefer `regex:` matches** for anything variable. `Find Text` / `Match Text`
  accept `regex:<pattern>`; use it instead of brittle exact strings when the
  text contains values that vary or that OCR renders inconsistently.
- **Scope with a `region`** when a short string risks matching elsewhere on
  screen, or when OCR is unreliable over the full frame.
- **Give realistic timeouts.** `Match Text` waits up to its timeout for text to
  appear; remote/network-dependent screens use generous timeouts (e.g. `120`)
  in the existing tests — match that, don't use the short default for slow
  screens.
- **Don't match the command echo.** When checking terminal output, use the
  `base64 cmd-finished` trick (above) so you don't match the typed command
  before it has run.

## Running and reading results

Run a single test (or omit the file to run all) using the existing script. It automatically loads the
broker's `.env` file (e.g. `e2e-tests-google.env`), so no extra wrapper is needed:

```bash
e2e-tests/run-tests.sh \
    --broker authd-google --release noble ./e2e-tests/tests/login_gdm.robot
```

**Read the live stderr trace, not `log.html`.** `listener/Listener.py` prints a
color-coded, real-time trace of every suite/test/keyword as it runs: each
keyword with its arguments, ✓/✗ status, timing, every `INFO`+ log message, and
the failure message on the failing test line. When a test fails, the trace shows
you the exact keyword that failed and the argument (e.g. the `Match Text` string
that timed out) — that is normally all you need to locate the problem. The
`output.xml` / `log.html` / recorded video in the output dir are there if you
need to dig deeper.
