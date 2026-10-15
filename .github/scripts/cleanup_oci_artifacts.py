#!/usr/bin/env python3
"""Delete GHCR versions older than the retention window."""

from __future__ import annotations

import json
import os
import sys
import time
from datetime import datetime, timedelta, timezone
from email.utils import parsedate_to_datetime
from io import BytesIO
from typing import Any, Iterator
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, urlopen

API_BASE = "https://api.github.com"
RETENTION_DAYS = 14
MAX_ERROR_BODY_BYTES = 1024
MAX_ATTEMPTS = 3
RETRY_BACKOFF_SECONDS = 1
RATE_LIMIT_FALLBACK_DELAY_SECONDS = 60
PACKAGE_SLUGS = (
    "authd-msentraid-snap",
    "authd-google-snap",
    "authd-deb-noble",
    "authd-deb-sources-noble",
    "authd-deb-resolute",
    "authd-deb-sources-resolute",
    "authd-deb-devel",
    "authd-deb-sources-devel",
    "e2e-runner",
)


def parse_timestamp(value: str) -> datetime:
    timestamp = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if timestamp.tzinfo is None:
        raise ValueError(f"Timestamp has no timezone: {value}")
    return timestamp.astimezone(timezone.utc)


def is_deletion_candidate(version: dict[str, Any], cutoff: datetime) -> bool:
    return parse_timestamp(version["updated_at"]) <= cutoff


def _read_http_error_body(error: HTTPError) -> bytes:
    if error.fp is None:
        return b""
    body = error.read()
    error.fp = BytesIO(body)
    return body


def _is_retryable_http_error(error: HTTPError) -> bool:
    if error.code == 408 or error.code == 429 or 500 <= error.code < 600:
        return True
    if error.code != 403:
        return False

    headers = error.headers
    if headers is not None and (
        headers.get("Retry-After") is not None
        or headers.get("X-RateLimit-Remaining") == "0"
    ):
        return True
    return b"rate limit" in _read_http_error_body(error).lower()


def _retry_delay(error: HTTPError, retry_index: int) -> float:
    headers = error.headers
    if headers is not None:
        retry_after = headers.get("Retry-After")
        if retry_after is not None:
            try:
                return max(0.0, float(retry_after))
            except ValueError:
                try:
                    retry_at = parsedate_to_datetime(retry_after)
                except (TypeError, ValueError, OverflowError):
                    pass
                else:
                    if retry_at.tzinfo is None:
                        retry_at = retry_at.replace(tzinfo=timezone.utc)
                    return max(
                        0.0, (retry_at - datetime.now(timezone.utc)).total_seconds()
                    )

        if headers.get("X-RateLimit-Remaining") == "0":
            reset = headers.get("X-RateLimit-Reset")
            if reset is not None:
                try:
                    return max(0.0, float(reset) - time.time())
                except ValueError:
                    pass

    if error.code in (403, 429):
        return RATE_LIMIT_FALLBACK_DELAY_SECONDS
    return RETRY_BACKOFF_SECONDS * 2**retry_index


def next_page_url(link_header: str | None) -> str | None:
    if not link_header:
        return None
    for link in link_header.split(","):
        url, separator, attributes = link.strip().partition(";")
        if separator and 'rel="next"' in attributes:
            return url.strip().removeprefix("<").removesuffix(">")
    return None


class GitHubApi:
    def __init__(self, token: str):
        self.headers = {
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "authd-oci-artifact-cleanup",
            "X-GitHub-Api-Version": "2022-11-28",
        }

    def request(self, url: str, method: str = "GET") -> tuple[Any, str | None]:
        for attempt in range(MAX_ATTEMPTS):
            request = Request(url, headers=self.headers, method=method)
            try:
                with urlopen(request, timeout=60) as response:
                    body = response.read()
                    data = json.loads(body) if body else None
                    return data, response.headers.get("Link")
            except HTTPError as error:
                if attempt == MAX_ATTEMPTS - 1 or not _is_retryable_http_error(error):
                    raise
                delay = _retry_delay(error, attempt)
                error.close()
            except OSError:
                if attempt == MAX_ATTEMPTS - 1:
                    raise
                delay = RETRY_BACKOFF_SECONDS * 2**attempt
            time.sleep(delay)
        raise AssertionError("Request attempts exhausted unexpectedly")

    def pages(self, url: str, list_key: str | None = None) -> Iterator[dict[str, Any]]:
        while url:
            data, link_header = self.request(url)
            if list_key is None:
                items = data
            elif isinstance(data, dict):
                items = data.get(list_key)
            else:
                items = None
            if not isinstance(items, list):
                details = (
                    f"response type: {type(data).__name__}; "
                    f"item type: {type(items).__name__}"
                )
                if isinstance(data, dict) and "message" in data:
                    details += f"; message: {data['message']}"
                raise ValueError(
                    f"Expected a list in the GitHub API response for {url}; {details}"
                )
            yield from items
            url = next_page_url(link_header)


def report_http_error(action: str, error: HTTPError) -> None:
    body = error.read(MAX_ERROR_BODY_BYTES + 1)
    truncated = len(body) > MAX_ERROR_BODY_BYTES
    response_body = body[:MAX_ERROR_BODY_BYTES].decode("utf-8", errors="replace")
    if not response_body:
        response_body = "<empty response body>"
    elif truncated:
        response_body += "... <truncated>"
    error.close()
    print(
        f"Failed to {action}: GitHub API returned HTTP {error.code} "
        f"for {error.url}: {response_body}",
        file=sys.stderr,
    )


def report_api_error(action: str, error: HTTPError | OSError) -> None:
    if isinstance(error, HTTPError):
        report_http_error(action, error)
        return
    print(f"Failed to {action}: network error: {error}", file=sys.stderr)


def package_versions_url(owner: str, repository: str, package_slug: str) -> str:
    package_name = quote(f"{repository}/{package_slug}", safe="")
    return (
        f"{API_BASE}/orgs/{owner}/packages/container/{package_name}/versions"
        "?per_page=100"
    )


def main() -> int:
    repository = os.environ["GITHUB_REPOSITORY"]
    owner, repository_name = repository.split("/", maxsplit=1)
    now = datetime.now(timezone.utc)
    api = GitHubApi(os.environ["GH_TOKEN"])
    cutoff = now - timedelta(days=RETENTION_DAYS)

    found_candidates = False
    had_errors = False
    for package_slug in PACKAGE_SLUGS:
        package_candidates: list[dict[str, Any]] = []
        try:
            versions = api.pages(
                package_versions_url(owner, repository_name, package_slug)
            )
            for version in versions:
                if is_deletion_candidate(version, cutoff):
                    package_candidates.append(version)
        except (HTTPError, OSError) as error:
            report_api_error(
                f"list versions for {repository_name}/{package_slug}", error
            )
            had_errors = True
            continue
        found_candidates = found_candidates or bool(package_candidates)

        for version in package_candidates:
            package_name = quote(f"{repository_name}/{package_slug}", safe="")
            delete_url = (
                f"{API_BASE}/orgs/{owner}/packages/container/{package_name}/versions/"
                f"{version['id']}"
            )
            try:
                api.request(delete_url, method="DELETE")
            except (HTTPError, OSError) as error:
                report_api_error(
                    f"delete {repository_name}/{package_slug} version {version['id']}",
                    error,
                )
                had_errors = True
                continue
            tags = version.get("metadata", {}).get("container", {}).get("tags", [])
            print(
                f"Deleted {repository_name}/{package_slug} version {version['id']} "
                f"updated at {version['updated_at']} "
                f"(tags: {', '.join(tags) or 'none'})."
            )

    if not found_candidates and not had_errors:
        print("No package versions were older than the retention window.")
    return 1 if had_errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
