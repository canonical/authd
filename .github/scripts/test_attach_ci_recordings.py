import json
import subprocess
import tempfile
import unittest
from contextlib import nullcontext
from pathlib import Path
from unittest.mock import patch

from attach_ci_recordings import attach_recordings, published_issues, recording_path

REPO = "canonical/authd"
FAILURE = {"key": "123:2", "run_id": 123, "attempt": 2}
REQUEST = {
    "issue": "aw_example",
    "failure_key": FAILURE["key"],
    "artifact": "e2e-tests-noble-google-123-2",
    "path": "suite/VM_Recording_failed_test.mp4",
}
ITEMS = [
    {"type": "create_issue", "temporary_id": "aw_example"},
    {"type": "update_issue", "issue_number": 42, "operation": "append"},
]
OUTCOMES = [
    {
        "type": item["type"],
        "messageIndex": index,
        "status": "success",
        "url": f"https://github.com/{REPO}/issues/42",
    }
    for index, item in enumerate(ITEMS)
]


class AttachmentTests(unittest.TestCase):
    def upload(
        self,
        requests,
        *,
        fail_first=False,
        video=b"recording",
        edit_during_download=False,
        expect_failure=False,
    ):
        bodies = {
            42: "Maintainer's description.\n\n[Recording](https://logs.example/video.mp4)\n"
        }
        calls = []

        def gh(*args):
            calls.append(args)
            if args[:2] == ("run", "download"):
                directory = Path(args[args.index("--dir") + 1])
                path = directory / REQUEST["path"]
                path.parent.mkdir(parents=True)
                path.write_bytes(video)
                if edit_during_download:
                    bodies[42] += "\nA maintainer added more evidence.\n"
                return ""
            if args[:2] == ("issue", "view"):
                return json.dumps({"body": bodies[int(args[2])]})
            if args[:2] == ("issue", "edit"):
                self.assertEqual(args[args.index("--repo") + 1], REPO)
                body = Path(args[args.index("--body-file") + 1]).read_text()
                path = Path(args[args.index("--attach") + 1])
                self.assertEqual(path.read_bytes(), video)
                if (
                    fail_first
                    and len([call for call in calls if call[:2] == ("issue", "edit")])
                    == 1
                ):
                    bodies[int(args[2])] = body
                    raise subprocess.CalledProcessError(
                        1, args, output="unsupported authentication type"
                    )
                bodies[int(args[2])] = (
                    body + "\nhttps://github.com/user-attachments/assets/abc-123\n"
                )
                return ""
            self.fail(f"Unexpected command: {args}")

        with (
            tempfile.TemporaryDirectory() as directory,
            patch("attach_ci_recordings.gh", side_effect=gh),
            patch("attach_ci_recordings.report_error") as error,
        ):
            with (
                self.assertRaisesRegex(ValueError, "triage is incomplete")
                if expect_failure
                else nullcontext()
            ):
                attach_recordings(
                    {"failures": [FAILURE]},
                    ITEMS,
                    OUTCOMES,
                    requests,
                    REPO,
                    Path(directory),
                )
            return calls, bodies, error.call_args_list

    def test_attaches_to_new_and_existing_issues_without_replaying_video(self):
        calls, bodies, errors = self.upload(
            [REQUEST, {**REQUEST, "issue": 42}, REQUEST]
        )
        self.assertFalse(errors)
        self.assertEqual(
            len([call for call in calls if call[:2] == ("issue", "edit")]), 1
        )
        self.assertEqual(
            len([call for call in calls if call[:2] == ("run", "download")]), 1
        )
        self.assertTrue(bodies[42].startswith("Maintainer's description.\n"))
        self.assertIn("[Recording](https://logs.example/video.mp4)", bodies[42])
        self.assertIn(
            "\nhttps://github.com/user-attachments/assets/abc-123\n", bodies[42]
        )
        self.assertNotIn(".mp4)", bodies[42].split("<!-- ci-triage-recording:", 1)[1])

    def test_failed_upload_keeps_link_and_incomplete_marker_does_not_skip_retry(self):
        calls, bodies, errors = self.upload(
            [REQUEST, REQUEST], fail_first=True, expect_failure=True
        )
        self.assertEqual(len(errors), 1)
        self.assertIn("unsupported authentication type", errors[0].args[0])
        self.assertEqual(
            len([call for call in calls if call[:2] == ("issue", "edit")]), 2
        )
        self.assertIn("[Recording](https://logs.example/video.mp4)", bodies[42])
        self.assertIn("https://github.com/user-attachments/assets/", bodies[42])

    def test_preserves_issue_edits_made_while_downloading(self):
        _, bodies, errors = self.upload([REQUEST], edit_during_download=True)
        self.assertFalse(errors)
        self.assertIn("A maintainer added more evidence.", bodies[42])
        self.assertIn("https://github.com/user-attachments/assets/", bodies[42])

    def test_unpublished_issues_and_wrong_attempts_are_not_written(self):
        for overrides in (
            {"issue": 99},
            {"failure_key": "123:1"},
            {"artifact": "e2e-tests-noble-google-123-1"},
        ):
            with self.subTest(overrides=overrides):
                calls, _, errors = self.upload(
                    [{**REQUEST, **overrides}], expect_failure=True
                )
                self.assertEqual(calls, [])
                self.assertEqual(len(errors), 1)

    def test_missing_and_oversized_recordings_fail_triage(self):
        for request, limit in (
            ({**REQUEST, "path": "missing.mp4"}, 100),
            (REQUEST, 3),
            ({**REQUEST, "path": "../recording.mp4"}, 100),
        ):
            with (
                self.subTest(request=request, limit=limit),
                patch("attach_ci_recordings.MAX_VIDEO_BYTES", limit),
            ):
                calls, bodies, errors = self.upload([request], expect_failure=True)
                self.assertFalse(any(call[:2] == ("issue", "edit") for call in calls))
                self.assertEqual(len(errors), 1)
                self.assertNotIn("ci-triage-recording:", bodies[42])

    def test_no_available_recordings_need_no_uploads(self):
        calls, _, errors = self.upload([])
        self.assertEqual(calls, [])
        self.assertEqual(errors, [])

    def test_too_many_requests_are_not_silently_truncated(self):
        with patch("attach_ci_recordings.MAX_RECORDINGS", 1):
            with self.assertRaisesRegex(ValueError, "At most 1"):
                self.upload([REQUEST, REQUEST])

    def test_only_successful_same_repository_issue_outputs_authorize_uploads(self):
        for overrides in (
            {"status": "failed"},
            {"status": "staged"},
            {"staged": True},
            {"url": "https://github.com/other/repo/issues/42"},
            {"url": f"https://github.com/{REPO}/pull/42"},
            {"type": "create_pull_request"},
            {"messageIndex": True},
        ):
            with self.subTest(overrides=overrides):
                self.assertEqual(
                    published_issues(ITEMS, [{**OUTCOMES[0], **overrides}], REPO), {}
                )

    def test_symlink_outside_artifact_is_not_uploaded(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            outside = root / "outside.mp4"
            outside.write_bytes(b"recording")
            artifact = root / "artifact"
            artifact.mkdir()
            (artifact / "recording.mp4").symlink_to(outside)
            with self.assertRaisesRegex(ValueError, "outside its artifact"):
                recording_path(artifact, "recording.mp4")


if __name__ == "__main__":
    unittest.main()
