import json
import os
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

from collect_ci_failures import COMPLETION_STEP, collect, pages


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


class CollectTests(unittest.TestCase):
    def collect(self, runs, attempts=None, jobs=None, checkpoints=(), current=CURRENT):
        starts = {run["id"]: run["run_started_at"] for run in runs}
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

    def test_lock_contains_completion_step(self):
        lock = Path(__file__).parents[1] / "workflows/nightly-ci-triage.lock.yml"
        self.assertIn(f"      - name: {COMPLETION_STEP}\n", lock.read_text())

    def test_incomplete_safe_outputs_keep_window_for_retry(self):
        workflows = Path(__file__).parents[1] / "workflows"
        scripts = []
        for suffix in ("md", "lock.yml"):
            workflow = (workflows / f"nightly-ci-triage.{suffix}").read_text()
            job = workflow.split("\n  verify_triage_writes:\n", 1)[1]
            job = job.split("\n\n", 1)[0].split("\n---", 1)[0]
            self.assertIn("    if: always()\n", job)
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
                    continue
                self.assertIn(error, checked.stdout)
                result = self.collect([previous, failure], checkpoints=(90,))
                self.assertTrue(result["bootstrap"])
                self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")
                self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

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
    def test_pagination_is_not_limited_to_first_page(self, api):
        api.side_effect = [
            {"workflow_runs": list(range(100))},
            {"workflow_runs": [100]},
        ]
        self.assertEqual(
            list(pages("repos/o/r/actions/runs", "workflow_runs")), list(range(101))
        )
        self.assertEqual(
            api.call_args.args[0], "repos/o/r/actions/runs?per_page=100&page=2"
        )


if __name__ == "__main__":
    unittest.main()
