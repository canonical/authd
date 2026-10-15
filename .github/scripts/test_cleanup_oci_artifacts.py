import io
import json
import os
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime, timedelta, timezone
from email.message import Message
from pathlib import Path
from unittest.mock import MagicMock, call, patch
from urllib.error import HTTPError, URLError

sys.path.insert(0, str(Path(__file__).parent))

from cleanup_oci_artifacts import (  # noqa: E402, I001
    GitHubApi,
    MAX_ERROR_BODY_BYTES,
    PACKAGE_SLUGS,
    is_deletion_candidate,
    main,
    next_page_url,
    report_http_error,
)


NOW = datetime(2026, 10, 8, 12, tzinfo=timezone.utc)
CUTOFF = NOW - timedelta(days=14)


def timestamp(value: datetime) -> str:
    return value.isoformat().replace("+00:00", "Z")


def version(
    created_at: datetime, tags: list[str], updated_at: datetime | None = None
) -> dict:
    return {
        "created_at": timestamp(created_at),
        "updated_at": timestamp(updated_at if updated_at is not None else created_at),
        "metadata": {"container": {"tags": tags}},
    }


def api_error(status: int, message: str, headers: Message | None = None) -> HTTPError:
    return HTTPError(
        "https://api.github.com/test",
        status,
        "Request failed",
        headers,
        io.BytesIO(json.dumps({"message": message}).encode()),
    )


def api_response(body: bytes = b'{"ok": true}') -> MagicMock:
    response = MagicMock()
    response.__enter__.return_value = response
    response.read.return_value = body
    response.headers.get.return_value = None
    return response


class CleanupSelectionTest(unittest.TestCase):
    def test_deletes_versions_older_than_the_retention_window(self):
        self.assertTrue(
            is_deletion_candidate(
                version(NOW - timedelta(days=15), ["sha-tag"]),
                CUTOFF,
            )
        )

    def test_keeps_versions_created_within_the_retention_window(self):
        self.assertFalse(
            is_deletion_candidate(
                version(NOW - timedelta(days=13), ["sha-tag"]),
                CUTOFF,
            )
        )

    def test_keeps_old_versions_that_were_recently_tagged(self):
        self.assertFalse(
            is_deletion_candidate(
                version(
                    NOW - timedelta(days=15),
                    ["current-sha-tag"],
                    updated_at=NOW - timedelta(days=1),
                ),
                CUTOFF,
            )
        )

    def test_deletes_versions_at_the_retention_cutoff(self):
        self.assertTrue(is_deletion_candidate(version(CUTOFF, []), CUTOFF))


class CleanupPaginationTest(unittest.TestCase):
    def test_next_page_url_extracts_the_next_relation(self):
        link_header = (
            '<https://api.github.com/packages?page=2>; rel="next", '
            '<https://api.github.com/packages?page=5>; rel="last"'
        )

        self.assertEqual(
            next_page_url(link_header), "https://api.github.com/packages?page=2"
        )

    def test_pages_follows_next_link_and_yields_every_page(self):
        first_url = "https://api.github.com/packages?page=1"
        second_url = "https://api.github.com/packages?page=2"
        api = GitHubApi("token")
        with patch.object(
            api,
            "request",
            side_effect=[
                ([{"id": 1}], f'<{second_url}>; rel="next"'),
                ([{"id": 2}], None),
            ],
        ) as request:
            result = list(api.pages(first_url))

        self.assertEqual(result, [{"id": 1}, {"id": 2}])
        self.assertEqual(
            [call.args[0] for call in request.call_args_list],
            [first_url, second_url],
        )


class CleanupRetryTest(unittest.TestCase):
    def test_request_retries_network_errors_with_backoff(self):
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[URLError("temporary network failure"), api_response()],
            ) as urlopen,
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            result = api.request("https://api.github.com/test")

        self.assertEqual(result, ({"ok": True}, None))
        self.assertEqual(urlopen.call_count, 2)
        sleep.assert_called_once_with(1)

    def test_request_retries_delete_after_server_error(self):
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[
                    api_error(500, "temporary failure"),
                    api_response(body=b""),
                ],
            ) as urlopen,
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            result = api.request("https://api.github.com/test", method="DELETE")

        self.assertEqual(result, (None, None))
        self.assertEqual(urlopen.call_count, 2)
        self.assertTrue(
            all(
                request_call.args[0].get_method() == "DELETE"
                for request_call in urlopen.call_args_list
            )
        )
        sleep.assert_called_once_with(1)

    def test_request_honors_retry_after_header(self):
        headers = Message()
        headers["Retry-After"] = "7"
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[
                    api_error(429, "secondary rate limit", headers),
                    api_response(),
                ],
            ),
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            result = api.request("https://api.github.com/test")

        self.assertEqual(result, ({"ok": True}, None))
        sleep.assert_called_once_with(7.0)

    def test_request_honors_rate_limit_reset_header(self):
        headers = Message()
        headers["X-RateLimit-Remaining"] = "0"
        headers["X-RateLimit-Reset"] = "1010"
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[
                    api_error(403, "rate limit exceeded", headers),
                    api_response(),
                ],
            ),
            patch("cleanup_oci_artifacts.time.time", return_value=1000),
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            result = api.request("https://api.github.com/test")

        self.assertEqual(result, ({"ok": True}, None))
        sleep.assert_called_once_with(10.0)

    def test_request_retries_secondary_rate_limit_without_headers(self):
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[
                    api_error(403, "You have exceeded a secondary rate limit."),
                    api_response(),
                ],
            ),
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            result = api.request("https://api.github.com/test")

        self.assertEqual(result, ({"ok": True}, None))
        sleep.assert_called_once_with(60)

    def test_request_stops_after_the_maximum_attempts(self):
        api = GitHubApi("token")
        with (
            patch(
                "cleanup_oci_artifacts.urlopen",
                side_effect=[api_error(500, "temporary failure") for _ in range(3)],
            ) as urlopen,
            patch("cleanup_oci_artifacts.time.sleep") as sleep,
        ):
            with self.assertRaises(HTTPError) as error:
                api.request("https://api.github.com/test")

        self.assertEqual(urlopen.call_count, 3)
        self.assertEqual(sleep.call_args_list, [call(1), call(2)])
        error.exception.close()


class CleanupApiErrorTest(unittest.TestCase):
    def test_report_http_error_truncates_large_response_bodies(self):
        error = HTTPError(
            "https://api.github.com/test",
            500,
            "Request failed",
            None,
            io.BytesIO(b"x" * (MAX_ERROR_BODY_BYTES + 100)),
        )
        stderr = io.StringIO()

        with redirect_stderr(stderr):
            report_http_error("make request", error)

        output = stderr.getvalue()
        self.assertIn("x" * MAX_ERROR_BODY_BYTES, output)
        self.assertIn("... <truncated>", output)
        self.assertNotIn("x" * (MAX_ERROR_BODY_BYTES + 1), output)
        self.assertLess(len(output), MAX_ERROR_BODY_BYTES + 256)

    def test_deletes_package_candidates_before_listing_next_package(self):
        now = datetime.now(timezone.utc)
        page_results = iter(
            [
                [dict(version(now - timedelta(days=30), ["stale"]), id=1)],
                [dict(version(now - timedelta(days=30), ["stale"]), id=2)],
            ]
        )
        events = []
        stdout = io.StringIO()

        def list_versions(url):
            events.append(("list", url))
            return next(page_results)

        def delete_version(url, method):
            events.append(("delete", url))
            return None, None

        with (
            patch.dict(
                os.environ,
                {"GITHUB_REPOSITORY": "canonical/authd", "GH_TOKEN": "token"},
            ),
            patch("cleanup_oci_artifacts.PACKAGE_SLUGS", ("first", "second")),
            patch("cleanup_oci_artifacts.GitHubApi") as github_api,
            redirect_stdout(stdout),
        ):
            api = github_api.return_value
            api.pages.side_effect = list_versions
            api.request.side_effect = delete_version
            result = main()

        self.assertEqual(result, 0)
        self.assertEqual(
            [event[0] for event in events], ["list", "delete", "list", "delete"]
        )
        self.assertIn("authd%2Ffirst", events[0][1])
        self.assertIn("/versions/1", events[1][1])
        self.assertIn("authd%2Fsecond", events[2][1])
        self.assertIn("/versions/2", events[3][1])

    def test_pages_reports_response_type_and_api_message(self):
        api = GitHubApi("token")
        with patch.object(
            api,
            "request",
            return_value=({"message": "rate limit exceeded"}, None),
        ):
            with self.assertRaises(ValueError) as error:
                list(api.pages("https://api.github.com/test"))

        self.assertIn("response type: dict", str(error.exception))
        self.assertIn("message: rate limit exceeded", str(error.exception))

    def test_main_continues_after_http_errors_and_returns_failure(self):
        now = datetime.now(timezone.utc)
        old_versions = [
            dict(version(now - timedelta(days=30), ["stale"]), id=1),
            dict(version(now - timedelta(days=30), ["stale"]), id=2),
        ]
        pages_side_effect = [
            api_error(403, "rate limit exceeded"),
            old_versions,
            URLError("temporary network failure"),
            *[[] for _ in PACKAGE_SLUGS[3:]],
        ]
        stderr = io.StringIO()
        stdout = io.StringIO()

        with (
            patch.dict(
                os.environ,
                {"GITHUB_REPOSITORY": "canonical/authd", "GH_TOKEN": "token"},
            ),
            patch("cleanup_oci_artifacts.GitHubApi") as github_api,
            redirect_stderr(stderr),
            redirect_stdout(stdout),
        ):
            api = github_api.return_value
            api.pages.side_effect = pages_side_effect
            api.request.side_effect = [
                api_error(500, "temporary failure"),
                (None, None),
            ]
            result = main()

        self.assertEqual(result, 1)
        self.assertEqual(api.pages.call_count, len(PACKAGE_SLUGS))
        self.assertEqual(api.request.call_count, 2)
        self.assertIn("HTTP 403", stderr.getvalue())
        self.assertIn("rate limit exceeded", stderr.getvalue())
        self.assertIn("temporary network failure", stderr.getvalue())
        self.assertIn("HTTP 500", stderr.getvalue())
        self.assertIn("temporary failure", stderr.getvalue())
        self.assertIn("Deleted authd/authd-google-snap version 2", stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
