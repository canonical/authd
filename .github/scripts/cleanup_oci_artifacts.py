#!/usr/bin/env python3
"""Delete GHCR versions older than the retention window."""

from __future__ import annotations

import json
import os
from datetime import datetime, timedelta, timezone
from typing import Any, Iterator
from urllib.parse import quote
from urllib.request import Request, urlopen

API_BASE = "https://api.github.com"
RETENTION_DAYS = 7
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
    return parse_timestamp(version["updated_at"]) < cutoff


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
        request = Request(url, headers=self.headers, method=method)
        with urlopen(request, timeout=60) as response:
            body = response.read()
            data = json.loads(body) if body else None
            return data, response.headers.get("Link")

    def pages(self, url: str, list_key: str | None = None) -> Iterator[dict[str, Any]]:
        while url:
            data, link_header = self.request(url)
            items = data if list_key is None else data.get(list_key)
            if not isinstance(items, list):
                raise ValueError(
                    f"Expected a list in the GitHub API response for {url}"
                )
            yield from items
            url = next_page_url(link_header)


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

    candidates: list[tuple[str, dict[str, Any]]] = []
    for package_slug in PACKAGE_SLUGS:
        versions = api.pages(package_versions_url(owner, repository_name, package_slug))
        for version in versions:
            if is_deletion_candidate(version, cutoff):
                candidates.append((package_slug, version))

    for package_slug, version in candidates:
        package_name = quote(f"{repository_name}/{package_slug}", safe="")
        delete_url = (
            f"{API_BASE}/orgs/{owner}/packages/container/{package_name}/versions/"
            f"{version['id']}"
        )
        api.request(delete_url, method="DELETE")
        tags = version.get("metadata", {}).get("container", {}).get("tags", [])
        print(
            f"Deleted {repository_name}/{package_slug} version {version['id']} "
            f"updated at {version['updated_at']} "
            f"(tags: {', '.join(tags) or 'none'})."
        )

    if not candidates:
        print("No package versions were older than the retention window.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
