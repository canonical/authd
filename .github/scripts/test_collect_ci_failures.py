import contextlib
import io
import os
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

from collect_ci_failures import COMPLETION_STEP, api, collect, pages


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
        by_id = {item["id"]: item for item in runs}
        return collect(
            runs,
            current,
            "main",
            lambda run_id, number: attempts[(run_id, number)],
            lambda run_id, number: (
                [
                    {
                        "started_at": by_id[run_id]["run_started_at"],
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

    def test_unusable_agent_output_keeps_window_for_retry(self):
        workflow = (
            Path(__file__).parents[1] / "workflows/nightly-ci-triage.lock.yml"
        ).read_text()
        self.assertIn(f"- name: {COMPLETION_STEP}", workflow)
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
        for content, exported, succeeds in [
            ('{"items":[{"type":"noop","message":"No failures"}]}', True, True),
            (None, True, False),
            (None, False, False),
            ("", True, False),
            ('{"items":[{"type":"create_issue","title":"CI failure"}]}', False, False),
        ]:
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
                self.assertEqual(checked.returncode == 0, succeeds, checked.stdout)
                if succeeds:
                    continue
                self.assertIn("::error::Required agent output", checked.stdout)
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

    def test_carried_over_completion_job_keeps_original_attempt_window(self):
        original = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="failure",
            created_at="2026-10-18T03:00:00Z",
            run_started_at="2026-10-18T03:00:00Z",
            updated_at="2026-10-18T04:00:00Z",
        )
        retried = {
            **original,
            "conclusion": "success",
            "run_attempt": 2,
            "run_started_at": "2026-10-20T03:00:00Z",
            "updated_at": "2026-10-20T04:00:00Z",
        }
        failure = run(
            2,
            created_at="2026-10-19T12:00:00Z",
            updated_at="2026-10-19T14:00:00Z",
        )
        carried_over = {
            "started_at": "2026-10-18T03:00:00Z",
            "steps": [{"name": COMPLETION_STEP, "conclusion": "success"}],
        }
        failed_job = {
            "id": 1,
            "name": "Tests",
            "html_url": "https://example.test/job/1",
            "conclusion": "failure",
            "steps": [{"name": "Run tests", "conclusion": "failure"}],
        }

        result = collect(
            [retried, failure],
            CURRENT,
            "main",
            lambda run_id, number: original,
            lambda run_id, number: [carried_over] if run_id == 90 else [failed_job],
        )
        self.assertFalse(result["bootstrap"])
        self.assertEqual(result["since"], "2026-10-18T03:00:00+00:00")
        self.assertEqual([item["key"] for item in result["failures"]], ["2:1"])

    def test_skipped_latest_attempt_is_not_a_checkpoint(self):
        original = run(
            90,
            workflow_id=20,
            event="schedule",
            conclusion="failure",
            created_at="2026-10-18T03:00:00Z",
            run_started_at="2026-10-18T03:00:00Z",
            updated_at="2026-10-18T04:00:00Z",
        )
        skipped = {
            **original,
            "conclusion": "success",
            "run_attempt": 2,
            "run_started_at": "2026-10-20T03:00:00Z",
            "updated_at": "2026-10-20T04:00:00Z",
        }
        failure = run(
            2,
            created_at="2026-10-19T12:00:00Z",
            updated_at="2026-10-19T14:00:00Z",
        )
        completion_job = {
            "started_at": "2026-10-18T03:00:00Z",
            "steps": [{"name": COMPLETION_STEP, "conclusion": "success"}],
        }
        skipped_job = {
            "started_at": None,
            "steps": [{"name": COMPLETION_STEP, "conclusion": "skipped"}],
        }
        failed_job = {
            "id": 1,
            "name": "Tests",
            "html_url": "https://example.test/job/1",
            "conclusion": "failure",
            "steps": [{"name": "Run tests", "conclusion": "failure"}],
        }

        def get_jobs(run_id, number):
            if run_id != 90:
                return [failed_job]
            return [skipped_job] if number == 2 else [completion_job]

        result = collect(
            [skipped, failure],
            CURRENT,
            "main",
            lambda run_id, number: original,
            get_jobs,
        )
        self.assertTrue(result["bootstrap"])
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

    @patch("collect_ci_failures.time.sleep")
    @patch("collect_ci_failures.subprocess.check_output")
    def test_api_retries_transient_failure(self, check_output, sleep):
        check_output.side_effect = [
            subprocess.CalledProcessError(1, "gh", stderr="gh: HTTP 502"),
            '{"ok": true}',
        ]
        self.assertEqual(api("repos/o/r"), {"ok": True})
        self.assertEqual(check_output.call_count, 2)
        sleep.assert_called_once()

    @patch("collect_ci_failures.time.sleep")
    @patch("collect_ci_failures.subprocess.check_output")
    def test_api_does_not_retry_missing_resource(self, check_output, sleep):
        check_output.side_effect = subprocess.CalledProcessError(
            1, "gh", stderr="gh: Not Found (HTTP 404)"
        )
        with (
            self.assertRaises(subprocess.CalledProcessError),
            contextlib.redirect_stderr(io.StringIO()) as stderr,
        ):
            api("repos/o/r")
        self.assertEqual(check_output.call_count, 1)
        sleep.assert_not_called()
        self.assertIn("Not Found", stderr.getvalue())

    @patch("collect_ci_failures.time.sleep")
    @patch("collect_ci_failures.subprocess.check_output")
    def test_api_gives_up_after_the_last_attempt(self, check_output, sleep):
        check_output.side_effect = subprocess.CalledProcessError(
            1, "gh", stderr="gh: HTTP 502"
        )
        with (
            self.assertRaises(subprocess.CalledProcessError),
            contextlib.redirect_stderr(io.StringIO()) as stderr,
        ):
            api("repos/o/r")
        self.assertEqual(check_output.call_count, 3)
        self.assertEqual(sleep.call_count, 2)
        self.assertIn("HTTP 502", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
