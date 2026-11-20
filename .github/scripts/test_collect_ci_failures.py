import json
import os
import stat
import subprocess
import tempfile
import textwrap
import unittest
from io import BytesIO
from itertools import product
from pathlib import Path
from time import monotonic
from unittest.mock import patch
from urllib.parse import parse_qs, urlencode, urlsplit
from zipfile import ZIP_BZIP2, ZipFile, ZipInfo

from collect_ci_failures import (
    COMPLETION_STEP,
    api,
    branch_runs,
    collect,
    collect_evidence,
    command_error,
    extract_diagnostics,
    main,
    timestamp,
)


def run(run_id=1, **overrides):
    return {
        "id": run_id,
        "workflow_id": 10,
        "name": "QA",
        "path": ".github/workflows/qa.yaml",
        "event": "push",
        "head_branch": "main",
        "head_sha": "abc",
        "status": "completed",
        "conclusion": "failure",
        "created_at": "2026-10-19T12:00:00Z",
        "run_started_at": "2026-10-19T12:00:00Z",
        "updated_at": "2026-10-21T02:00:00Z",
        "run_attempt": 1,
        "html_url": f"https://github.com/canonical/authd/actions/runs/{run_id}",
        **overrides,
    }


CURRENT = run(
    100,
    workflow_id=20,
    event="schedule",
    run_started_at="2026-10-21T03:00:00Z",
)
REPOSITORY = {"default_branch": "main", "created_at": "2023-06-19T08:26:41Z"}
RUNS_QUERY = urlencode(
    {
        "branch": "main",
        "created": f"{REPOSITORY['created_at']}..{CURRENT['run_started_at']}",
        "per_page": 100,
        "page": 1,
    }
)


class CollectTests(unittest.TestCase):
    def collect(self, runs, attempts=None, jobs=None, checkpoints=(), current=CURRENT):
        starts = {run["id"]: run["run_started_at"] for run in runs}
        attempts = {
            **{(run["id"], run["run_attempt"]): run for run in runs},
            **(attempts or {}),
        }
        return collect(
            runs,
            current,
            "main",
            lambda run_id, number: attempts[(run_id, number)],
            lambda run_id, number: (
                [
                    {
                        "started_at": starts[run_id],
                        "steps": [{"name": COMPLETION_STEP, "conclusion": "success"}],
                    }
                ]
                if run_id in checkpoints
                else jobs or []
            ),
        )

    def test_failed_triage_does_not_advance_checkpoint(self):
        previous = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            run_started_at="2026-10-18T03:00:00Z",
            updated_at="2026-10-18T04:00:00Z",
        )
        failed_triage = run(95, workflow_id=20, event="schedule")
        failure = run(2, updated_at="2026-10-19T14:00:00Z")
        result = self.collect([previous, failed_triage, failure], checkpoints=(90,))
        self.assertFalse(result["bootstrap"])
        self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_checkpoint_metadata_update_does_not_widen_window(self):
        previous = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            created_at="2026-10-19T03:00:00Z",
            run_started_at="2026-10-19T03:00:00Z",
            updated_at="2026-10-19T04:00:00Z",
        )
        latest = run(
            95,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            created_at="2026-10-20T03:00:00Z",
            run_started_at="2026-10-20T03:00:00Z",
        )
        failure = run(2, updated_at="2026-10-19T14:00:00Z")
        for updated_at in (
            CURRENT["run_started_at"],
            "2026-10-21T04:00:00Z",
            "2027-10-21T04:00:00Z",
        ):
            with self.subTest(updated_at=updated_at):
                result = self.collect(
                    [previous, {**latest, "updated_at": updated_at}, failure],
                    checkpoints=(90, 95),
                )
                self.assertFalse(result["bootstrap"])
                self.assertEqual(result["since"], "2026-10-20T03:00:00+00:00")
                self.assertEqual(result["failures"], [])

    def test_checkpoint_retry_requires_completion_job_from_current_attempt(self):
        previous = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            created_at="2026-10-20T02:00:00Z",
            run_started_at="2026-10-20T02:00:00Z",
            updated_at="2026-10-20T03:00:00Z",
        )
        retry = run(
            95,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            created_at="2026-10-21T02:00:00Z",
            run_started_at="2026-10-21T10:00:00Z",
            updated_at="2026-10-21T10:30:00Z",
            run_attempt=2,
        )
        current = {**CURRENT, "run_started_at": "2026-10-22T03:00:00Z"}
        failure = run(
            2,
            created_at="2026-10-21T02:15:00Z",
            updated_at="2026-10-21T02:30:00Z",
        )
        for started_at, expected_since, expected_failures in [
            ("2026-10-21T02:00:00Z", previous["run_started_at"], ["2:1"]),
            (None, previous["run_started_at"], ["2:1"]),
            (retry["run_started_at"], retry["run_started_at"], []),
            ("2026-10-21T10:00:01Z", retry["run_started_at"], []),
        ]:
            with self.subTest(started_at=started_at):
                completion_job = {
                    "run_attempt": 2,
                    "conclusion": "success",
                    "steps": [{"name": COMPLETION_STEP, "conclusion": "success"}],
                }
                if started_at is not None:
                    completion_job["started_at"] = started_at
                result = self.collect(
                    [previous, retry, failure],
                    jobs=[completion_job],
                    checkpoints=(90,),
                    current=current,
                )
                self.assertFalse(result["bootstrap"])
                self.assertEqual(result["since"], expected_since.replace("Z", "+00:00"))
                self.assertEqual(
                    [item["key"] for item in result["failures"]], expected_failures
                )

    def test_completion_requires_the_collector_manifest(self):
        lock = (
            Path(__file__).parents[1] / "workflows/nightly-ci-triage.lock.yml"
        ).read_text()
        manifest = b'{"failures": [{"key": "42:1"}]}'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def run_step(name, **env):
                step = lock.split(f"name: {name}\n", 1)[1]
                script = json.loads(step.split("        run: ", 1)[1].splitlines()[0])
                return subprocess.run(
                    [
                        "bash",
                        "-euo",
                        "pipefail",
                        "-c",
                        script.replace("/tmp/gh-aw/ci-triage", directory),
                    ],
                    env={**os.environ, **env},
                    capture_output=True,
                    text=True,
                    check=False,
                )

            step_output = root / "step-output"
            (root / "failures.json").write_bytes(manifest)
            result = run_step(
                "Record CI failure manifest digest", GITHUB_OUTPUT=str(step_output)
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            digest = step_output.read_text().removeprefix("manifest_sha256=").strip()
            completed = {"42:1": "Already tracked"}
            cases = (
                ("complete", manifest, completed, digest, True),
                ("missing digest", manifest, completed, "", False),
                ("changed manifest", b'{"failures": []}', {}, digest, False),
                ("incomplete", manifest, {}, digest, False),
            )
            for label, contents, summaries, expected, success in cases:
                with self.subTest(label=label):
                    (root / "failures.json").write_bytes(contents)
                    (root / "completed.json").write_text(json.dumps(summaries))
                    result = run_step(
                        COMPLETION_STEP, EXPECTED_MANIFEST_SHA256=expected
                    )
                    self.assertEqual(result.returncode == 0, success, result.stderr)

    def test_incomplete_safe_outputs_keep_window_for_retry(self):
        workflows = Path(__file__).parents[1] / "workflows"
        scripts = []
        for suffix in ("md", "lock.yml"):
            workflow = (workflows / f"nightly-ci-triage.{suffix}").read_text()
            job = workflow.split("\n  verify_triage_writes:\n", 1)[1]
            job = job.split("\n\n", 1)[0].split("\n---", 1)[0]
            self.assertIn(
                "    if: always() && github.repository == 'canonical/authd'\n", job
            )
            needs = job.split("    needs:", 1)[1].split("    if:", 1)[0]
            self.assertIn("agent", needs)
            self.assertIn("safe_outputs", needs)
            step = job.split(
                "      - name: Require all triage writes before advancing the checkpoint\n",
                1,
            )[1]
            scripts.append(
                textwrap.dedent(
                    step.split("        run: |\n", 1)[1].split("        env:\n", 1)[0]
                ).rstrip()
            )
            for variable, expression in {
                "SAFE_OUTPUTS_RESULT": "needs.safe_outputs.result",
                "SAFE_OUTPUTS_STATUS": "needs.safe_outputs.outputs.process_safe_outputs_status",
                **{
                    f"ITEMS_{outcome.upper()}": (
                        f"needs.safe_outputs.outputs.process_safe_outputs_items_{outcome}"
                    )
                    for outcome in ("deferred", "skipped", "failed", "cancelled")
                },
            }.items():
                self.assertIn(f"{variable}: ${{{{ {expression} }}}}", step)
        self.assertEqual(*scripts)
        complete = {
            "SAFE_OUTPUTS_RESULT": "success",
            "SAFE_OUTPUTS_STATUS": "success",
            "ITEMS_DEFERRED": "0",
            "ITEMS_SKIPPED": "0",
            "ITEMS_FAILED": "0",
            "ITEMS_CANCELLED": "0",
        }
        cases = [{}]
        cases.extend({variable: ""} for variable in complete)
        cases.extend(
            {variable: "1"} for variable in complete if variable.startswith("ITEMS_")
        )
        cases.extend(
            {variable: value}
            for variable in ("SAFE_OUTPUTS_RESULT", "SAFE_OUTPUTS_STATUS")
            for value in (
                "failure",
                "skipped",
                "cancelled",
                "deferred",
                "partial_success",
            )
        )
        for overrides in cases:
            with self.subTest(overrides=overrides):
                checked = subprocess.run(
                    ["bash", "-euo", "pipefail", "-c", scripts[1]],
                    env={**os.environ, **complete, **overrides},
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertEqual(checked.returncode == 0, not overrides, checked.stdout)
                previous = run(
                    90,
                    workflow_id=20,
                    event="schedule",
                    conclusion="success" if checked.returncode == 0 else "failure",
                    created_at="2026-10-19T03:00:00Z",
                    run_started_at="2026-10-19T03:00:00Z",
                )
                result = self.collect([previous], checkpoints=(90,))
                self.assertEqual(result["bootstrap"], bool(overrides))
                if overrides:
                    self.assertIn("::error::Incomplete safe outputs", checked.stdout)
                    self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")

    def test_unusable_agent_output_keeps_window_for_retry(self):
        workflow = (
            Path(__file__).parents[1] / "workflows/nightly-ci-triage.lock.yml"
        ).read_text()
        safe_outputs = workflow.split("\n  safe_outputs:\n", 1)[1]
        guard_name = "      - name: Require agent output before applying safe outputs\n"
        before, guard = safe_outputs.split(guard_name, 1)
        guard, after = guard.split("      - name: ", 1)
        self.assertIn("Download patch artifact", before)
        self.assertTrue(after.startswith("Process Safe Outputs\n"))
        script = textwrap.dedent(
            guard.split("        run: |\n", 1)[1].split("        env:\n", 1)[0]
        )
        previous = run(
            90,
            workflow_id=20,
            event="schedule",
            created_at="2026-10-19T03:00:00Z",
            run_started_at="2026-10-19T03:00:00Z",
        )
        failure = run(
            2,
            created_at="2026-10-18T12:00:00Z",
            updated_at="2026-10-18T14:00:00Z",
        )
        missing_output = "::error::Required agent output"
        invalid_update = "::error::Only append issue body updates are allowed"
        cases = [
            ('{"items":[{"type":"noop","message":"No failures"}]}', True, None),
            ('{"items":[{"type":"update_issue","operation":"append"}]}', True, None),
            ('{"items":[{"type":"update_issue"}]}', True, None),
            (
                '{"items":[{"type":"create_issue"},{"type":"update_issue","operation":"replace"}]}',
                True,
                invalid_update,
            ),
            (
                '{"items":[{"type":"update_issue","operation":"prepend"}]}',
                True,
                invalid_update,
            ),
            (None, True, missing_output),
            (None, False, missing_output),
            ("", True, missing_output),
            (
                '{"items":[{"type":"create_issue","title":"CI failure"}]}',
                False,
                missing_output,
            ),
        ]
        invalid_type = "::error::Each created issue needs one Bug type call"
        create = {"type": "create_issue", "temporary_id": "aw_bug1"}
        bug_type = {
            "type": "set_issue_type",
            "issue_number": "aw_bug1",
            "issue_type": "Bug",
        }
        cases.extend(
            [
                (json.dumps({"items": [create, bug_type]}), True, None),
                (
                    json.dumps(
                        {
                            "items": [
                                {**create, "temporary_id": "#aw_bug1"},
                                {**bug_type, "issue_number": "AW_BUG1"},
                            ]
                        }
                    ),
                    True,
                    None,
                ),
                (
                    json.dumps({"items": [create, {**bug_type, "suggest": False}]}),
                    True,
                    None,
                ),
                (
                    json.dumps(
                        {
                            "items": [
                                {**create, "temporary_id": "123"},
                                {**bug_type, "issue_number": "123"},
                            ]
                        }
                    ),
                    True,
                    invalid_type,
                ),
                (
                    json.dumps({"items": [create, {**bug_type, "suggest": True}]}),
                    True,
                    invalid_type,
                ),
                (json.dumps({"items": [create]}), True, invalid_type),
                (json.dumps({"items": [bug_type, create]}), True, None),
                (
                    json.dumps({"items": [create, bug_type, bug_type]}),
                    True,
                    invalid_type,
                ),
                (
                    json.dumps(
                        {"items": [{**create, "temporary_id": "aw_bug2"}, bug_type]}
                    ),
                    True,
                    invalid_type,
                ),
                (
                    json.dumps({"items": [{**bug_type, "issue_type": ""}]}),
                    True,
                    invalid_type,
                ),
                (
                    json.dumps({"items": [{**bug_type, "issue_type": "Feature"}]}),
                    True,
                    invalid_type,
                ),
                (
                    json.dumps(
                        {
                            "items": [
                                {"type": "set_issue_type", "issue_number": "aw_bug1"}
                            ]
                        }
                    ),
                    True,
                    invalid_type,
                ),
            ]
        )
        for field in ("status", "state", "title", "labels", "assignees", "milestone"):
            for value in ("closed", None, False, [], ""):
                for operation in ({}, {"operation": "append"}):
                    cases.append(
                        (
                            json.dumps(
                                {
                                    "items": [
                                        {"type": "noop", "message": "No failures"},
                                        {
                                            "type": "update_issue",
                                            **operation,
                                            field: value,
                                        },
                                    ]
                                }
                            ),
                            True,
                            invalid_update,
                        )
                    )
        for content, exported, error in cases:
            with (
                self.subTest(content=content, exported=exported),
                tempfile.TemporaryDirectory() as directory,
            ):
                output = Path(directory) / "agent_output.json"
                if content is not None:
                    output.write_text(content)
                checked = subprocess.run(
                    [
                        "bash",
                        "-euo",
                        "pipefail",
                        "-c",
                        script.replace("/tmp/gh-aw", directory),
                    ],
                    env={
                        **os.environ,
                        "GH_AW_AGENT_OUTPUT": str(output) if exported else "",
                    },
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertEqual(checked.returncode == 0, error is None, checked.stdout)
                if error is None:
                    items = json.loads(output.read_text())["items"]
                    for index, item in enumerate(items):
                        if item["type"] != "create_issue":
                            continue
                        typed = items[index + 1]
                        self.assertEqual(typed["type"], "set_issue_type")
                        self.assertEqual(
                            typed["issue_number"].lstrip("#").lower(),
                            item["temporary_id"].lstrip("#").lower(),
                        )
                    continue
                if content is not None:
                    self.assertEqual(output.read_text(), content)
                self.assertIn(error, checked.stdout)
                result = self.collect([previous, failure], checkpoints=(90,))
                self.assertTrue(result["bootstrap"])
                self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")
                self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_triage_type_calls_follow_their_creations(self):
        workflow = (
            Path(__file__).parents[1] / "workflows/nightly-ci-triage.lock.yml"
        ).read_text()
        guard = workflow.split(
            "      - name: Require agent output before applying safe outputs\n", 1
        )[1].split("      - name: ", 1)[0]
        script = textwrap.dedent(
            guard.split("        run: |\n", 1)[1].split("        env:\n", 1)[0]
        )
        create = {"type": "create_issue", "temporary_id": "#aw_snapcdn"}
        bug_type = {
            "type": "set_issue_type",
            "issue_number": "#aw_snapcdn",
            "issue_type": "Bug",
        }
        other_create = {**create, "temporary_id": "aw_other"}
        other_type = {**bug_type, "issue_number": "AW_OTHER"}
        pr = {"type": "create_pull_request", "body": "Fixes #aw_snapcdn"}
        update = {"type": "update_issue", "operation": "append", "body": "Occurrence"}
        cases = [
            ([bug_type, create, pr], [create, bug_type, pr]),
            (
                [other_type, bug_type, create, update, other_create, pr],
                [create, bug_type, update, other_create, other_type, pr],
            ),
        ]
        for queued, expected in cases:
            with (
                self.subTest(queued=queued),
                tempfile.TemporaryDirectory() as directory,
            ):
                output = Path(directory) / "agent_output.json"
                output.write_text(json.dumps({"items": queued, "errors": []}))
                checked = subprocess.run(
                    [
                        "bash",
                        "-euo",
                        "pipefail",
                        "-c",
                        script.replace("/tmp/gh-aw", directory),
                    ],
                    env={**os.environ, "GH_AW_AGENT_OUTPUT": str(output)},
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
                self.assertEqual(
                    json.loads(output.read_text()), {"items": expected, "errors": []}
                )

    def test_first_failed_triage_window_is_retried(self):
        previous = run(
            90,
            workflow_id=20,
            event="schedule",
            created_at="2026-10-19T03:00:00Z",
        )
        failure = run(
            2,
            created_at="2026-10-18T12:00:00Z",
            updated_at="2026-10-18T14:00:00Z",
        )
        result = self.collect([previous, failure])
        self.assertTrue(result["bootstrap"])
        self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_same_bootstrap_run_retry_preserves_original_window(self):
        original = {
            **CURRENT,
            "created_at": "2026-10-19T03:00:00Z",
            "run_started_at": "2026-10-19T03:00:00Z",
            "updated_at": "2026-10-19T04:00:00Z",
        }
        retry = {
            **original,
            "run_started_at": CURRENT["run_started_at"],
            "updated_at": CURRENT["updated_at"],
            "run_attempt": 2,
        }
        failure = run(
            2,
            created_at="2026-10-18T12:00:00Z",
            updated_at="2026-10-18T14:00:00Z",
        )
        initial = self.collect([original, failure], current=original)
        result = self.collect(
            [retry, failure], {(CURRENT["id"], 1): original}, current=retry
        )
        self.assertTrue(result["bootstrap"])
        self.assertEqual(result["since"], initial["since"])
        self.assertEqual(result["until"], "2026-10-21T03:00:00+00:00")
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_success_without_completed_triage_is_not_a_checkpoint(self):
        skipped = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="success",
            created_at="2026-10-19T03:00:00Z",
            run_started_at="2026-10-19T03:00:00Z",
        )
        result = self.collect([skipped])
        self.assertTrue(result["bootstrap"])
        self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")

    def test_old_run_rerun_successfully_keeps_failed_attempt(self):
        rerun = run(2, run_attempt=2, conclusion="success")
        failed_attempt = run(2, updated_at="2026-10-20T15:00:00Z")
        result = self.collect([rerun], {(2, 1): failed_attempt})
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_metadata_update_does_not_make_an_old_failure_recent(self):
        old_attempt = run(
            2,
            created_at="2025-10-20T02:02:32Z",
            run_started_at="2025-10-20T02:02:32Z",
            updated_at="2025-10-20T02:03:54Z",
        )
        metadata_update = {
            **old_attempt,
            "updated_at": "2026-10-21T02:00:00Z",
        }
        result = self.collect([metadata_update], {(2, 1): old_attempt})
        self.assertEqual(result["failures"], [])

    def test_recent_failed_rerun_of_an_old_run_is_included(self):
        old_attempt = run(
            2,
            created_at="2025-10-20T02:02:32Z",
            run_started_at="2025-10-20T02:02:32Z",
            updated_at="2025-10-20T02:03:54Z",
        )
        rerun = {
            **old_attempt,
            "run_attempt": 2,
            "run_started_at": "2026-10-21T01:00:00Z",
            "updated_at": "2026-10-21T02:00:00Z",
        }
        result = self.collect([rerun], {(2, 1): old_attempt})
        self.assertEqual([item["key"] for item in result["failures"]], ["2:2"])

    def test_completion_window_and_default_branch_scope(self):
        runs = [
            run(1, updated_at="2026-10-20T03:00:00Z"),
            run(2, updated_at="2026-10-20T02:59:59Z"),
            run(3, updated_at="2026-10-21T03:00:00Z"),
            run(4, head_branch="feature"),
            run(5, event="pull_request"),
            run(6, event="pull_request_target"),
            run(7, status="in_progress", conclusion=None),
            run(8, conclusion="cancelled"),
        ]
        result = self.collect(runs)
        self.assertTrue(result["bootstrap"])
        self.assertEqual([item["key"] for item in result["failures"]], ["1:1"])

    def test_failed_job_in_successful_workflow_is_included(self):
        job = {
            "id": 123,
            "name": "Go tests",
            "html_url": "https://github.com/canonical/authd/actions/runs/1/job/123",
            "conclusion": "failure",
            "steps": [{"name": "Run tests", "conclusion": "failure"}],
        }
        result = self.collect([run(conclusion="success")], jobs=[job])
        self.assertEqual(result["failures"][0]["jobs"][0]["steps"], ["Run tests"])

    def test_running_rerun_keeps_completed_failed_attempt(self):
        rerun = run(2, run_attempt=2, status="in_progress", conclusion=None)
        result = self.collect([rerun], {(2, 1): run(2)})
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    @patch("collect_ci_failures.api")
    def test_branch_scan_splits_capped_ranges_and_keeps_every_run(self, api):
        start = "2025-01-01T00:00:00Z"
        before_split = "2025-01-01T00:00:01Z"
        after_split = "2025-01-01T00:00:02Z"
        end = "2025-01-01T00:00:03Z"
        runs = [
            {"id": i, "created_at": before_split if i < 500 else after_split}
            for i in range(1001)
        ]

        def fetch(endpoint, *, deadline):
            query = parse_qs(urlsplit(endpoint).query)
            self.assertEqual(query["branch"], ["release/26.04"])
            first, last = query["created"][0].split("..")
            matches = [run for run in runs if first <= run["created_at"] <= last][:1000]
            size = int(query["per_page"][0])
            offset = (int(query["page"][0]) - 1) * size
            return {
                "total_count": len(matches),
                "workflow_runs": matches[offset : offset + size],
            }

        api.side_effect = fetch
        listed = branch_runs(
            "repos/o/r/actions/runs",
            "release/26.04",
            timestamp(start),
            timestamp(end),
            deadline=monotonic() + 30,
        )
        self.assertEqual(list(listed), runs)

    def test_metadata_request_retries_only_one_bad_gateway_within_deadline(self):
        endpoint = f"repos/canonical/authd/actions/runs?{RUNS_QUERY}"
        page = {"total_count": 1, "workflow_runs": [run(42)]}
        for status, expires, recovers, requests in (
            (502, False, True, 2),
            (502, False, False, 2),
            (502, True, False, 1),
            (403, False, False, 1),
        ):
            with self.subTest(status=status, expires=expires, recovers=recovers):
                error = subprocess.CalledProcessError(
                    1, ["gh", "api", endpoint], stderr=f"gh: HTTP {status}".encode()
                )
                replies = iter(
                    (error, json.dumps(page).encode() if recovers else error)
                )
                now = 0

                def fetch(*args, **kwargs):
                    nonlocal now
                    now = 30 if expires else 29
                    reply = next(replies)
                    if reply is error:
                        raise error
                    return reply

                with (
                    patch("collect_ci_failures.monotonic", side_effect=lambda: now),
                    patch(
                        "collect_ci_failures.subprocess.check_output", side_effect=fetch
                    ) as request,
                ):
                    if recovers:
                        self.assertEqual(api(endpoint, deadline=30), page)
                    else:
                        with self.assertRaises(subprocess.CalledProcessError) as raised:
                            api(endpoint, deadline=30)
                        self.assertIs(raised.exception, error)
                    self.assertEqual(request.call_count, requests)
                    if requests == 2:
                        self.assertEqual(request.call_args.kwargs["timeout"], 1)

    def test_scan_deadline_prevents_publishing_a_partial_manifest(self):
        repo = "canonical/authd"
        base = f"repos/{repo}/actions/runs"
        attempt = f"{base}/42/attempts/1"
        jobs = f"{attempt}/jobs?per_page=100&page=1"
        responses = {
            f"{base}/100/attempts/1": CURRENT,
            f"repos/{repo}": REPOSITORY,
            f"{base}?{RUNS_QUERY}": {"total_count": 1, "workflow_runs": [run(42)]},
            attempt: run(42),
            jobs: {"jobs": []},
        }
        now = 0

        def request(command, *, timeout, **kwargs):
            nonlocal now
            endpoint = command[-1]
            if endpoint == attempt:
                now = 12 * 60 - 1
            if endpoint == jobs and timeout < 2:
                raise subprocess.TimeoutExpired(command, timeout)
            return json.dumps(responses[endpoint])

        with (
            tempfile.TemporaryDirectory() as directory,
            patch.dict(
                os.environ,
                GITHUB_REPOSITORY=repo,
                GITHUB_RUN_ID="100",
                GITHUB_RUN_ATTEMPT="1",
            ),
            patch("collect_ci_failures.monotonic", side_effect=lambda: now),
            patch("collect_ci_failures.subprocess.check_output", side_effect=request),
        ):
            output = Path(directory)
            with self.assertRaises(subprocess.TimeoutExpired):
                main(output)
            self.assertFalse((output / "failures.json").exists())

    def test_command_error_reports_value_error_message(self):
        self.assertEqual(command_error(ValueError("search cap")), "search cap")
        self.assertEqual(command_error(KeyError("jobs")), "KeyError")


class EvidenceTests(unittest.TestCase):
    @staticmethod
    def job(job_id, name="tests", attempt=2, **kwargs):
        return {
            "id": job_id,
            "name": name,
            "attempt": attempt,
            "run_attempt": attempt,
            "html_url": f"https://example.test/job/{job_id}",
            "conclusion": "failure",
            **kwargs,
        }

    def e2e_job(self, job_id, release, attempt, started_at):
        return self.job(
            job_id,
            f"provision-and-run ({release}) / e2e-test (authd-msentraid) / "
            "Run e2e-tests (authd-msentraid)",
            attempt,
            started_at=started_at,
        )

    @staticmethod
    def failure(run_id=42, path=".github/workflows/qa.yaml", jobs=()):
        return {"run_id": run_id, "attempt": 2, "path": path, "jobs": list(jobs)}

    @staticmethod
    def artifact(artifact_id, name, **kwargs):
        return {"id": artifact_id, "name": name, "size_in_bytes": 1024, **kwargs}

    @staticmethod
    def write_archive(destination, files):
        with ZipFile(destination, "w") as archive:
            for name, contents in files.items():
                archive.writestr(name, contents)

    def test_logs_are_sanitized_and_failed_download_leaves_no_partial_file(self):
        good, bad = self.job(10), self.job(11)
        failure = self.failure(jobs=(good, bad))

        def download(command, **kwargs):
            if command[-1].endswith("/10/logs"):
                kwargs["stdout"].write(b"\x1b[31mred\x1b[0m\x00 text\nnext\x07 line\n")
                return
            kwargs["stdout"].write(b"partial")
            raise subprocess.CalledProcessError(
                1, command, stderr=b"HTTP 403: https://signed.example/secret"
            )

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            output = Path(directory)
            collect_evidence("canonical/authd", [failure], output, monotonic() + 30)
            self.assertEqual(
                Path(good["log_path"]).read_text(), "red text\nnext line\n"
            )
            self.assertEqual(bad["log_error"], "Job log unavailable (HTTP 403)")
            self.assertNotIn("log_path", bad)
            self.assertFalse((output / "logs/42-2/11.log").exists())

    def test_main_persists_artifacts_for_failed_jobs_across_reruns(self):
        repo = "canonical/authd"
        base = f"repos/{repo}/actions/runs"
        first = run(
            42,
            path=".github/workflows/e2e-tests.yaml@main",
            created_at="2026-10-20T08:00:00Z",
            run_started_at="2026-10-20T08:00:00Z",
            updated_at="2026-10-20T09:00:00Z",
        )
        second = {
            **first,
            "run_attempt": 2,
            "run_started_at": "2026-10-20T10:00:00Z",
            "updated_at": "2026-10-20T11:00:00Z",
        }
        noble = self.e2e_job(10, "noble", 1, first["run_started_at"])
        retained = {**noble, "id": 20, "run_attempt": 2}
        stonking = self.e2e_job(21, "stonking", 2, second["run_started_at"])
        resolute = self.e2e_job(22, "resolute", 2, second["run_started_at"])
        noble_name = "e2e-tests-noble-authd-msentraid-42-1"
        resolute_name = "e2e-tests-resolute-authd-msentraid-42-2"
        unrelated = "e2e-tests-jammy-authd-msentraid-42-2"
        endpoints = {
            f"repos/{repo}": REPOSITORY,
            f"{base}/100/attempts/1": CURRENT,
            f"{base}?{RUNS_QUERY}": {"total_count": 1, "workflow_runs": [second]},
            f"{base}/42/attempts/1": first,
            f"{base}/42/attempts/2": second,
            f"{base}/42/attempts/1/jobs?per_page=100&page=1": {"jobs": [noble]},
            f"{base}/42/attempts/2/jobs?per_page=100&page=1": {
                "jobs": [retained, stonking, resolute]
            },
            f"{base}/42/artifacts?per_page=100&page=1": {
                "artifacts": [self.artifact(i, f"irrelevant-{i}") for i in range(99)]
                + [self.artifact(100, unrelated)]
            },
            f"{base}/42/artifacts?per_page=100&page=2": {
                "artifacts": [
                    self.artifact(101, noble_name),
                    self.artifact(102, resolute_name),
                ]
            },
        }
        archives = []

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/logs"):
                stdout.write(b"job log\n")
                return
            artifact_id = int(endpoint.split("/")[-2])
            archives.append(artifact_id)
            self.write_archive(
                stdout,
                {
                    "output.xml": str(artifact_id),
                    "journal/vm.journal": b"journal\x00",
                    "Login/screenshots/failure.webp": b"image",
                    "recording.mp4": b"video",
                },
            )

        with (
            tempfile.TemporaryDirectory() as directory,
            patch.dict(
                os.environ,
                GITHUB_REPOSITORY=repo,
                GITHUB_RUN_ID="100",
                GITHUB_RUN_ATTEMPT="1",
            ),
            patch(
                "collect_ci_failures.api", side_effect=lambda path, **_: endpoints[path]
            ),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            output = Path(directory)
            main(output)
            one, two = json.loads((output / "failures.json").read_text())["failures"]
            self.assertEqual((one["key"], two["key"]), ("42:1", "42:2"))
            original = one["jobs"][0]
            inherited, missing, current = two["jobs"]
            self.assertEqual(inherited["attempt"], 1)
            self.assertEqual(inherited["artifact_path"], original["artifact_path"])
            self.assertEqual(
                Path(inherited["artifact_path"], "output.xml").read_text(), "101"
            )
            self.assertEqual(
                Path(current["artifact_path"], "output.xml").read_text(), "102"
            )
            self.assertEqual(
                Path(current["artifact_path"], "journal/vm.journal").read_bytes(),
                b"journal\x00",
            )
            screenshot = (
                Path(current["artifact_path"]) / "Login/screenshots/failure.webp"
            )
            self.assertEqual(screenshot.read_bytes(), b"image")
            self.assertFalse(Path(current["artifact_path"], "recording.mp4").exists())
            self.assertNotIn("artifact_path", missing)
            self.assertIn("stonking", missing["artifact_error"])
            self.assertEqual(archives, [101, 102])

    def test_e2e_stages_failed_suites_under_both_limits(self):
        name = "e2e-tests-noble-authd-msentraid-42-2"
        failed_journal = "Regenerate Qrcode/journal/vm.journal"
        failed_screenshot = "Regenerate Qrcode/screenshots/failure.webp"
        logs = {
            "listener": (
                b"2026-11-16T08:39:29.3228407Z Suite  Allowed Users  PASS  1m52s\n"
                b"2026-11-16T09:35:08.6649622Z \x1b[36mSuite  Regenerate Qrcode"
                b"  \x1b[31mFAIL\x1b[0m  2m44s  1 test, 0 passed, 1 failed\n"
            ),
            "partial": b"Suite  No Diagnostics  FAIL  1m2s\n",
            "missing": None,
        }
        stats = (
            "<robot><statistics><suite>"
            '<stat name="Allowed Users" pass="1" fail="0" skip="0">'
            "Tests.Allowed Users</stat>"
            '<stat name="Regenerate Qrcode" pass="0" fail="1" skip="0">'
            "Tests.Regenerate Qrcode</stat>"
            "</suite></statistics></robot>"
        )

        def download(command, *, stdout, **kwargs):
            if command[-1].endswith("/logs"):
                if (log := logs[log_source]) is None:
                    raise subprocess.CalledProcessError(1, command, stderr=b"HTTP 404")
                stdout.write(log)
            else:
                self.write_archive(
                    stdout,
                    {
                        "Allowed Users/journal/vm.journal": bytes(8),
                        "Allowed Users/screenshots/passing.webp": b"passing",
                        "Authd Disabled/journal/vm.journal": bytes(7),
                        failed_journal: b"failed",
                        failed_screenshot: b"screen",
                        "log.html": b"log",
                        "report.html": b"report",
                        "output.xml": stats if log_source != "listener" else "No stats",
                    },
                )

        for log_source, (budget, limit, error) in product(
            logs,
            (
                ("MAX_DIAGNOSTIC_BYTES", 15, "size limit"),
                ("MAX_ARCHIVE_FILES", 3, "3-file staging limit"),
            ),
        ):
            with (
                self.subTest(log=log_source, budget=budget),
                tempfile.TemporaryDirectory() as directory,
                patch(f"collect_ci_failures.{budget}", limit),
                patch(
                    "collect_ci_failures.api",
                    return_value={"artifacts": [self.artifact(50, name)]},
                ),
                patch("collect_ci_failures.subprocess.run", side_effect=download),
            ):
                job = self.e2e_job(10, "noble", 2, None)
                failure = self.failure(
                    path=".github/workflows/e2e-tests.yaml", jobs=[job]
                )
                collect_evidence(
                    "canonical/authd", [failure], Path(directory), monotonic() + 30
                )
                staged = Path(job["artifact_path"])
                self.assertCountEqual(
                    (
                        path.relative_to(staged).as_posix()
                        for path in staged.rglob("*")
                        if path.is_file()
                    ),
                    (failed_journal, failed_screenshot, "log.html"),
                )
                self.assertIn(error, job["artifact_error"])

    def test_unusable_robot_results_do_not_set_staging_priority(self):
        stats = (
            "<robot><statistics><suite>"
            '<stat name="Login" pass="0" fail="1" skip="0">Tests.Login</stat>'
            "</suite></statistics></robot>"
        )
        for results, limit in ((f"<!DOCTYPE robot>{stats}", 1024), (stats, 16)):
            raw = BytesIO()
            self.write_archive(
                raw,
                {
                    "log.html": b"log",
                    "report.html": b"report",
                    "Login/screenshots/failure.webp": b"image",
                    "output.xml": results,
                },
            )
            with (
                self.subTest(results_limit=limit),
                tempfile.TemporaryDirectory() as directory,
                patch("collect_ci_failures.MAX_ROBOT_RESULTS_BYTES", limit),
            ):
                output = Path(directory)
                extract_diagnostics(
                    raw, output, monotonic() + 30, 9, failed_suites=set()
                )
                self.assertEqual((output / "report.html").read_bytes(), b"report")
                self.assertFalse((output / "Login/screenshots/failure.webp").exists())

    def test_bad_archive_does_not_prevent_staging_other_diagnostics(self):
        bad = self.job(10, "Go Tests with Coverage Collection")
        good = self.job(11, "Go Tests with Race Detector")
        failure = self.failure(jobs=(bad, good))
        artifacts = [
            self.artifact(50, "authd-go-tests-coverage-artifacts-2"),
            self.artifact(51, "authd-go-tests-race-artifacts-2"),
        ]

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/logs"):
                stdout.write(b"log\n")
            elif endpoint.endswith("/50/zip"):
                stdout.write(b"broken zip")
            else:
                self.write_archive(
                    stdout, {"gorace.log.123": "race", "too-long.log": "x" * 20}
                )

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.MAX_DIAGNOSTIC_BYTES", 10),
            patch("collect_ci_failures.api", return_value={"artifacts": artifacts}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd", [failure], Path(directory), monotonic() + 30
            )
            self.assertNotIn("artifact_path", bad)
            self.assertIn("BadZipFile", bad["artifact_error"])
            path = Path(good["artifact_path"])
            self.assertEqual((path / "gorace.log.123").read_text(), "race")
            self.assertFalse((path / "too-long.log").exists())
            self.assertIn("size limit", good["artifact_error"])

    def test_unsafe_archive_members_are_not_staged(self):
        artifact = self.artifact(50, "authd-go-tests-race-artifacts-2")
        link = ZipInfo("link.log")
        link.external_attr = stat.S_IFLNK << 16
        compressed = ZipInfo("compressed.log")
        compressed.compress_type = ZIP_BZIP2
        first = ZipInfo("a.log")

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.MAX_ARCHIVE_FILES", 1),
            patch("collect_ci_failures.api", return_value={"artifacts": [artifact]}),
            patch("collect_ci_failures.subprocess.run"),
            patch("collect_ci_failures.zipfile.ZipFile") as reader,
        ):
            output = Path(directory)
            absolute = output / "absolute.log"
            archive = reader.return_value.__enter__.return_value

            def open_member(member):
                if member is first:
                    return BytesIO(b"log")
                raise AssertionError("member should not have been opened")

            archive.open.side_effect = open_member
            for prefix, member in product(
                ([], [first, ZipInfo("b.log")]),
                (
                    ZipInfo("../outside.log"),
                    ZipInfo(str(absolute)),
                    ZipInfo(r"reports\job.log"),
                    link,
                    compressed,
                ),
            ):
                with self.subTest(member=member.filename, after_cap=bool(prefix)):
                    archive.infolist.return_value = [*prefix, member]
                    job = self.job(10, "Go Tests with Race Detector")
                    collect_evidence(
                        "canonical/authd",
                        [self.failure(jobs=(job,))],
                        output,
                        monotonic() + 30,
                    )
                    self.assertIn("BadZipFile", job["artifact_error"])
                    self.assertNotIn("artifact_path", job)
                    self.assertFalse(any((output / "artifacts/42").iterdir()))
                    self.assertFalse(absolute.exists())

    def test_archive_member_limit_does_not_block_later_artifacts(self):
        first = self.job(10, "Go Tests with Coverage Collection")
        second = self.job(11, "Go Tests with Race Detector")
        artifacts = [
            self.artifact(50, "authd-go-tests-coverage-artifacts-2"),
            self.artifact(51, "authd-go-tests-race-artifacts-2"),
        ]

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/logs"):
                stdout.write(b"log\n")
            elif endpoint.endswith("/50/zip"):
                self.write_archive(stdout, {"a.log": "a", "b.log": "b", "c.log": "c"})
            else:
                self.write_archive(stdout, {"race.log": "report"})

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.MAX_ARCHIVE_MEMBERS", 2),
            patch("collect_ci_failures.api", return_value={"artifacts": artifacts}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd",
                [self.failure(jobs=(first, second))],
                Path(directory),
                monotonic() + 30,
            )
            self.assertNotIn("artifact_path", first)
            self.assertIn("2-member limit", first["artifact_error"])
            path = Path(second["artifact_path"])
            self.assertEqual((path / "race.log").read_text(), "report")

    def test_archive_file_limit_keeps_partial_and_later_diagnostics(self):
        first = self.job(10, "Go Tests with Coverage Collection")
        second = self.job(11, "Go Tests with Race Detector")
        artifacts = [
            self.artifact(50, "authd-go-tests-coverage-artifacts-2"),
            self.artifact(51, "authd-go-tests-race-artifacts-2"),
        ]

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/logs"):
                stdout.write(b"log\n")
            elif endpoint.endswith("/50/zip"):
                self.write_archive(stdout, {"a.log": "a", "b.log": "b"})
            else:
                self.write_archive(stdout, {"race.log": "report"})

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.MAX_ARCHIVE_FILES", 1),
            patch("collect_ci_failures.api", return_value={"artifacts": artifacts}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd",
                [self.failure(jobs=(first, second))],
                Path(directory),
                monotonic() + 30,
            )
            path = Path(first["artifact_path"])
            self.assertEqual((path / "a.log").read_text(), "a")
            self.assertFalse((path / "b.log").exists())
            self.assertIn("1-file staging limit", first["artifact_error"])
            path = Path(second["artifact_path"])
            self.assertEqual((path / "race.log").read_text(), "report")
            self.assertNotIn("artifact_error", second)

    def test_deadline_records_missing_evidence_instead_of_dropping_jobs(self):
        job = self.job(10, "Go Tests with Race Detector")
        failure = self.failure(jobs=(job,))
        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.subprocess.run") as download,
        ):
            collect_evidence(
                "canonical/authd", [failure], Path(directory), monotonic() - 1
            )
            self.assertIn("deadline", job["log_error"])
            self.assertIn("deadline", job["artifact_error"])
            download.assert_not_called()

    def test_slow_archive_can_finish_within_evidence_deadline(self):
        artifact = self.artifact(50, "authd-go-tests-race-artifacts-2")

        def download(command, *, stdout, timeout, **kwargs):
            if command[-1].endswith("/logs"):
                stdout.write(b"job log")
            elif timeout < 97:
                raise subprocess.TimeoutExpired(command, timeout)
            else:
                self.write_archive(stdout, {"race.log": "race report"})

        with (
            patch("collect_ci_failures.monotonic", return_value=0),
            patch("collect_ci_failures.api", return_value={"artifacts": [artifact]}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            for deadline, available in ((110, True), (80, False)):
                with (
                    self.subTest(deadline=deadline),
                    tempfile.TemporaryDirectory() as directory,
                ):
                    job = self.job(10, "Go Tests with Race Detector")
                    collect_evidence(
                        "canonical/authd",
                        [self.failure(jobs=(job,))],
                        Path(directory),
                        deadline,
                    )
                    if available:
                        self.assertEqual(
                            Path(job["artifact_path"], "race.log").read_text(),
                            "race report",
                        )
                    else:
                        self.assertIn("request timed out", job["artifact_error"])
                        self.assertNotIn("artifact_path", job)

    def test_slow_logs_leave_a_time_slice_for_artifacts(self):
        slow = self.job(10, "C Code sanity")
        artifact_job = self.job(11, "Go Tests with Race Detector")
        artifact = self.artifact(50, "authd-go-tests-race-artifacts-2")
        clock = [0.0]

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/10/logs"):
                stdout.write(b"slow log\n")
                clock[0] = 85  # past the log phase's 80-second slice
            elif endpoint.endswith("/11/logs"):
                raise AssertionError("the log phase should have ended")
            else:
                self.write_archive(stdout, {"race.log": "report"})

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.monotonic", side_effect=lambda: clock[0]),
            patch("collect_ci_failures.ARTIFACT_RESERVE_SECONDS", 20),
            patch("collect_ci_failures.api", return_value={"artifacts": [artifact]}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd",
                [self.failure(jobs=(slow, artifact_job))],
                Path(directory),
                100,
            )
            self.assertEqual(Path(slow["log_path"]).read_text(), "slow log\n")
            self.assertIn("deadline", artifact_job["log_error"])
            self.assertEqual(
                Path(artifact_job["artifact_path"], "race.log").read_text(),
                "report",
            )

    def test_jobs_without_logs_get_artifact_budget_first(self):
        first = self.job(10, "Go Tests with Race Detector")
        second = self.job(11, "Go Tests with Coverage Collection")
        third = self.job(12)
        archive = BytesIO()
        self.write_archive(archive, {"error.log": "artifact diagnostics"})
        payload = archive.getvalue()
        artifacts = [
            self.artifact(
                50, "authd-go-tests-race-artifacts-2", size_in_bytes=len(payload)
            ),
            self.artifact(
                51, "authd-go-tests-coverage-artifacts-2", size_in_bytes=len(payload)
            ),
        ]
        log = b"job diagnostics\n"

        def download(command, *, stdout, **kwargs):
            endpoint = command[-1]
            if endpoint.endswith("/11/logs"):
                raise subprocess.CalledProcessError(1, command, stderr=b"HTTP 404")
            stdout.write(log if endpoint.endswith("/logs") else payload)

        with (
            tempfile.TemporaryDirectory() as directory,
            patch(
                "collect_ci_failures.MAX_DOWNLOAD_BYTES", 2 * len(log) + len(payload)
            ),
            patch("collect_ci_failures.api", return_value={"artifacts": artifacts}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd",
                [self.failure(jobs=(first, second)), self.failure(43, jobs=(third,))],
                Path(directory),
                monotonic() + 30,
            )
            self.assertEqual(Path(first["log_path"]).read_bytes(), log)
            self.assertEqual(Path(third["log_path"]).read_bytes(), log)
            self.assertIn("HTTP 404", second["log_error"])
            self.assertEqual(
                Path(second["artifact_path"], "error.log").read_text(),
                "artifact diagnostics",
            )
            self.assertIn("size budget", first["artifact_error"])

    def test_staged_archive_respects_space_used_by_logs(self):
        job = self.job(10, "Go Tests with Race Detector")
        artifact = self.artifact(50, "authd-go-tests-race-artifacts-2")

        def download(command, *, stdout, **kwargs):
            if command[-1].endswith("/logs"):
                stdout.write(b"log\n")
            else:
                self.write_archive(stdout, {"first.log": "1234", "second.log": "5678"})

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("collect_ci_failures.MAX_STAGED_BYTES", 9),
            patch("collect_ci_failures.api", return_value={"artifacts": [artifact]}),
            patch("collect_ci_failures.subprocess.run", side_effect=download),
        ):
            collect_evidence(
                "canonical/authd",
                [self.failure(jobs=(job,))],
                Path(directory),
                monotonic() + 30,
            )
            self.assertEqual(Path(job["log_path"]).read_text(), "log\n")
            path = Path(job["artifact_path"])
            self.assertEqual((path / "first.log").read_text(), "1234")
            self.assertFalse((path / "second.log").exists())
            self.assertIn("size limit", job["artifact_error"])


if __name__ == "__main__":
    unittest.main()
