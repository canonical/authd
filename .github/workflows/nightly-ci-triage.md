---
name: Nightly CI triage
description: Track default-branch CI failures and propose fixes or diagnostics.
on:
  schedule:
    - cron: "0 3 * * *"
  workflow_dispatch:
permissions:
  contents: read
  actions: read
  checks: read
  issues: read
  pull-requests: read
  copilot-requests: write
concurrency:
  group: nightly-ci-triage
  cancel-in-progress: false
engine: copilot
timeout-minutes: 50
network:
  allowed:
    - defaults
    - github
    - go
    - rust
    - python
tools:
  github:
    mode: gh-proxy
    github-token: ${{ secrets.GITHUB_TOKEN }}
    toolsets: [repos, issues, pull_requests, actions]
safe-outputs:
  github-token: ${{ secrets.GITHUB_TOKEN }}
  steps:
    - name: Require agent output before applying safe outputs
      # The pinned runtime can restore the file without exporting its path.
      # Fail in that case so the next run retries the same window.
      env:
        GH_AW_AGENT_OUTPUT: ${{ steps.setup-agent-output-env.outputs.GH_AW_AGENT_OUTPUT }}
      run: |
        if [ "$GH_AW_AGENT_OUTPUT" != "/tmp/gh-aw/agent_output.json" ] || [ ! -s "$GH_AW_AGENT_OUTPUT" ]; then
          echo "::error::Required agent output is missing or its path was not exported; leave this window for the next run"
          exit 1
        fi
        if ! jq -e '
          all(.items[];
            .type != "update_issue" or
            ((.operation // "append") == "append" and
             (has("status") or has("state") or has("title") or
              has("labels") or has("assignees") or has("milestone") | not)))
        ' "$GH_AW_AGENT_OUTPUT" > /dev/null; then
          echo "::error::Only append issue body updates are allowed"
          exit 1
        fi
  create-issue:
    max: 20
    labels: [bug]
    deduplicate-by-title: false
  update-issue:
    target: "*"
    body: true
    max: 50
  create-pull-request:
    github-token-for-extra-empty-commit: none
    draft: true
    max: 5
    auto-close-issue: false
    fallback-as-issue: false
    if-no-changes: error
    protected-files: request-review
    excluded-files:
      - ".github/workflows/**"
      - ".github/scripts/collect_ci_failures.py"
      - ".github/scripts/test_collect_ci_failures.py"
    allowed-files:
      - ".github/actions/**"
      - ".github/scripts/**"
      - "cmd/**"
      - "internal/**"
      - "pam/**"
      - "nss/**"
      - "brokers/**"
      - "examplebroker/**"
      - "e2e-tests/**"
      - "scripts/**"
      - "debian/**"
      - "tests/**"
      - "Cargo.toml"
      - "Cargo.lock"
      - "go.mod"
      - "go.sum"
steps:
  - name: Collect CI failures
    env:
      GH_TOKEN: ${{ github.token }}
    run: python3 .github/scripts/collect_ci_failures.py
post-steps:
  - name: Require complete triage before advancing the checkpoint
    run: |
      python3 - <<'PY'
      import json
      from pathlib import Path

      root = Path("/tmp/gh-aw/ci-triage")
      failures = json.loads((root / "failures.json").read_text())["failures"]
      completed = json.loads((root / "completed.json").read_text())
      expected = {failure["key"] for failure in failures}
      if not isinstance(completed, dict) or set(completed) != expected:
          raise ValueError("Incomplete CI triage; leave this window for the next run")
      if not all(isinstance(value, str) and value.strip() for value in completed.values()):
          raise ValueError("Incomplete CI triage; leave this window for the next run")
      PY
  - name: Upload CI triage manifests
    if: always()
    uses: actions/upload-artifact@v7
    with:
      name: ci-triage-manifests
      path: /tmp/gh-aw/ci-triage/*.json
      if-no-files-found: ignore
jobs:
  verify_triage_writes:
    needs: [agent, safe_outputs]
    if: always()
    runs-on: ubuntu-slim
    permissions: {}
    steps:
      - name: Require all triage writes before advancing the checkpoint
        env:
          SAFE_OUTPUTS_RESULT: ${{ needs.safe_outputs.result }}
          SAFE_OUTPUTS_STATUS: ${{ needs.safe_outputs.outputs.process_safe_outputs_status }}
          ITEMS_DEFERRED: ${{ needs.safe_outputs.outputs.process_safe_outputs_items_deferred }}
          ITEMS_SKIPPED: ${{ needs.safe_outputs.outputs.process_safe_outputs_items_skipped }}
          ITEMS_FAILED: ${{ needs.safe_outputs.outputs.process_safe_outputs_items_failed }}
          ITEMS_CANCELLED: ${{ needs.safe_outputs.outputs.process_safe_outputs_items_cancelled }}
        run: |
          if [ "$SAFE_OUTPUTS_RESULT" != "success" ] || [ "$SAFE_OUTPUTS_STATUS" != "success" ] ||
             [ "$ITEMS_DEFERRED" != "0" ] || [ "$ITEMS_SKIPPED" != "0" ] ||
             [ "$ITEMS_FAILED" != "0" ] || [ "$ITEMS_CANCELLED" != "0" ]; then
            echo "::error::Incomplete safe outputs; leave this window for the next run"
            exit 1
          fi
---

# Nightly CI triage

Triage every failure in `/tmp/gh-aw/ci-triage/failures.json` for this repository.
The collector uses the start of the last successful, complete triage attempt
as its lower bound. A checkpoint requires the completion job to have started
in that attempt; jobs retained from an earlier attempt do not count.
Without a checkpoint, the window starts 24 hours before the earliest prior
triage run, or before the current run's first attempt if there are no prior
runs. Failed or skipped bootstrap runs keep that original lower bound, so
retries can cover more than 24 hours. The window is not capped, to avoid
dropping untriaged failures. It includes completed attempts by completion
time, including reruns of old runs and failed jobs in otherwise successful
workflows. Do not narrow this window or skip entries.

The agent has 50 minutes within the 60-minute job limit, leaving time for
setup, completion checks, and artifact uploads. The `ci-triage-manifests`
artifact retains `failures.json` and any `completed.json`, even on failure.
After safe-output processing, a final job requires successful processing with
no deferred, skipped, failed, or cancelled items. Missing results also fail
the run, leaving the window for retry even if the completion file is present.

## Investigate and group

Read `AGENTS.md` and the manifest first. Use the authenticated, read-only `gh`
CLI to fetch failed job logs, annotations, and relevant test artifacts. For
example, use `gh run view RUN_ID --attempt ATTEMPT --log-failed`, and the
attempt-specific Actions API when necessary. Inspect each failed job and each
independent failure inside it; do not assume one run has only one cause. If
there are no jobs, inspect the workflow/check annotations and configuration.
For successful workflows with failed jobs, inspect the failed jobs too.

Group occurrences by the actual error, test, component, and likely root cause.
Do not match solely on workflow names, generic exit codes, or similar titles.
Keep run attempt and job links so reruns remain distinguishable. Distinguish
transient infrastructure failures from code regressions, but track both.

## Match existing issues before creating anything

Search both open and closed issues and linked PRs using relevant error and
test-name searches. Paginate results; do not treat a small recent sample as an
exhaustive duplicate search. Inspect matching bodies and relevant comments.
Check whether a closed issue's fix was already present in the failing SHA.  Do
not treat every new occurrence after a fix as proof of the same root cause.

For a known issue, use `update_issue` with `operation: append` to edit its
description, not a comment. Append a concise dated occurrence list with the
failed attempt and job links and any new evidence. Preserve the original body,
title, labels, and open/closed state. Read the current body before queuing the
update and omit occurrences already recorded in it or its overflow issues
(see below). Batch all new occurrences for the same issue into one update when
they fit. Never replace a maintainer's description or reopen an issue. Known
issues do not need another fix PR, except to finish missing follow-up work from
an earlier incomplete triage run.

### Overflow for known issues

GitHub limits issue bodies to 65,536 characters. Before queuing an append,
count the full resulting body, including separators. Keep it at most 60,000
characters to leave room for runtime metadata. If the batch would exceed this
budget, leave the original description unchanged. Never truncate or remove
existing content to make room.

Store the new occurrences in continuation issues titled
`ci: Occurrences for #<original issue number> (part <N>)`. Each body must link
to the original issue and contain the exact marker
`ci-triage-overflow: OWNER/REPO#NUMBER`, with this repository and the original
issue number substituted. Write the marker as a visible plain-text line, not
an HTML comment, so body sanitization preserves it. Before creating or updating
a continuation, search open and closed issues for this marker text and paginate
all results. Read their bodies as well as the original body. Match by the exact
marker text and root cause, not the title alone.

For each occurrence, record its manifest failure `key`, specific error or test,
and attempt and job links in the destination body. Deduplicate against the
original issue and all its continuations on every run, including retries.
A key or run link alone does not mean every independent error was handled.
Reuse a matching continuation with room, batching one append per destination.
If none has room, create the next numbered part. Split large batches across
parts, keeping each resulting body within the same 60,000-character budget.
Do not queue an append that exceeds the budget.

Continuation issues are occurrence records for a known problem, not new
problems requiring another fix or diagnostics PR. They still consume the
20-create and 50-update limits. Include the original issue and each destination
reference in the completion summary. If any occurrence cannot be recorded
within those limits, report the blocker and leave the window for retry; do not
mark that occurrence handled or write the completion file.

### New issues

For a new issue, inspect relevant open and closed CI issues to match their
title and body style. Prefer the existing `ci: <specific symptom>` style when
it fits. Don't use `.github/ISSUE_TEMPLATE/bug_report.yml`. Create one issue
per distinct problem, not per run. Include the observable failure, affected
workflow/job/test, failed attempt and job links, commit SHA, an error excerpt,
and available reproduction details. Clearly separate facts, hypotheses, and
missing evidence.  Include every occurrence in this window.  Never file a
speculative root cause as established fact or publicly disclose a suspected
security vulnerability.  In each new issue, state the planned follow-up: a fix
PR, a diagnostics PR, or the specific reason no code change is useful. This
lets later runs finish missing follow-up work if issue creation succeeds but PR
creation fails.

## Propose a fix or diagnostics for each new issue

Inspect the source and open PRs. If the evidence supports a small, safe fix,
implement it with a focused regression test and open a draft PR. Keep each
independent fix in its own branch and PR, based on the default branch. Do not
stack unrelated fixes or include changes from another issue. Follow repository
instructions and run the smallest relevant tests and linters. Preserve behavior
for existing installations. Do not weaken checks, disable tests, hide errors,
or retry failures blindly to make CI green.

If the logs do not establish the cause, create a draft diagnostics PR instead
of guessing a fix. Add targeted output or failure artifacts at the point where
evidence is missing, so the next occurrence can distinguish concrete
hypotheses.  Prefer failure-only output, bounded logs, timestamps, test seeds,
safe version information, and relevant process state. Verify that diagnostic
output will actually be retained when the failing command exits nonzero, and
does not change the test's exit status or production behavior.

Use only the built-in GitHub Actions token. Do not modify files under
`.github/workflows/`. Prefer diagnostics in tests, source code, or existing
scripts. If a useful fix or diagnostic change requires editing a workflow file,
describe the exact proposed changes and their purpose in the issue for a
maintainer to apply. Treat that documented handoff as a completed triage
outcome, not a reason to retry the same window forever.

If a fix is not feasible for a reason other than missing logs (for example an
upstream outage requiring no useful code change), explain the blocker and next
action in the issue. If relevant diagnostics are already present or an open PR
already addresses this problem, link that evidence instead of duplicating work.

Read `.github/pull_request_template.md` and any
`.github/PULL_REQUEST_TEMPLATE/` templates before drafting PR bodies. Explain
the symptom, evidence, change, issue link, and exact validation commands with
results. State which checks could not run and why. Fix PRs may say `Fixes
<issue reference>`; diagnostics PRs must say `Related to <issue reference>` and
must not close the issue.

## Finish without losing work

Only after every failure and independent job error has a known-issue update
(in the original body or a continuation), a new issue and the appropriate PR,
or an explicit justified no-change outcome, write
`/tmp/gh-aw/ci-triage/completed.json`. It must be a JSON object mapping
every manifest failure `key` to a nonempty summary identifying its issue,
PR/output reference, or existing evidence. Do not include keys outside the
manifest. This file is mandatory even when the manifest is empty (`{}`).

If logs or tools are unavailable, record that uncertainty and add useful
diagnostics where feasible. If investigation cannot be completed, an output
limit is reached (20 new issues, 50 updates, 5 PRs), or a required PR cannot be
prepared, report the blocker and do not write the completion file. The post-step
will fail the run, leaving the window for the next nightly run. Already-created
issues, links, and PRs must be reused on retry, not created again.

If no writes are needed, call `noop` with a short reason. End with a concise
summary of the window, issue updates, new issues, PRs, and any blockers.
