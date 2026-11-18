#!/usr/bin/env python3
"""Collect completed CI failures without missing old runs that were rerun."""

import json
import os
import re
import subprocess
from datetime import datetime, timedelta
from pathlib import Path
from time import monotonic
from urllib.parse import urlencode

FAILURES = {"failure", "timed_out", "action_required", "startup_failure", "stale"}
COMPLETION_STEP = "Require complete triage before advancing the checkpoint"
SCAN_SECONDS = 12 * 60
REQUEST_SECONDS = 60
PAGE_SIZE = 100
FILTERED_RUN_LIMIT = 1000


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def api(endpoint, *, deadline):
    command = ["gh", "api", endpoint]
    for retry in (False, True):
        try:
            return json.loads(
                subprocess.check_output(
                    command, stderr=subprocess.PIPE, timeout=request_timeout(deadline)
                )
            )
        except subprocess.CalledProcessError as error:
            if retry or command_error(error) != "HTTP 502" or monotonic() >= deadline:
                raise


def pages(endpoint, key, *, deadline, first_page=None):
    separator = "&" if "?" in endpoint else "?"
    response = first_page
    page = 1
    while True:
        if response is None:
            request = f"{endpoint}{separator}per_page={PAGE_SIZE}&page={page}"
            response = api(request, deadline=deadline)
        items = response[key]
        yield from items
        if len(items) < PAGE_SIZE:
            return
        page += 1
        response = None


def branch_runs(endpoint, branch, start, end, *, deadline):
    created = f"{start:%Y-%m-%dT%H:%M:%SZ}..{end:%Y-%m-%dT%H:%M:%SZ}"
    query = f"{endpoint}?{urlencode({'branch': branch, 'created': created})}"
    first = api(f"{query}&per_page={PAGE_SIZE}&page=1", deadline=deadline)
    if first["total_count"] < FILTERED_RUN_LIMIT:
        yield from pages(query, "workflow_runs", deadline=deadline, first_page=first)
        return

    if start == end:
        raise ValueError("One second of workflow history reaches the GitHub search cap")
    middle = start + timedelta(seconds=int((end - start).total_seconds()) // 2)
    yield from branch_runs(endpoint, branch, start, middle, deadline=deadline)
    yield from branch_runs(
        endpoint, branch, middle + timedelta(seconds=1), end, deadline=deadline
    )


def collect(runs, current, branch, get_attempt, get_jobs):
    until = timestamp(current["run_started_at"])
    triage_runs = [
        run
        for run in runs
        if run["workflow_id"] == current["workflow_id"]
        and run["id"] != current["id"]
        and run["head_branch"] == branch
        and run["event"] in {"schedule", "workflow_dispatch"}
        and timestamp(run["created_at"]) < until
    ]
    successful = sorted(
        (
            run
            for run in triage_runs
            if (
                run["conclusion"] == "success"
                and timestamp(run["run_started_at"]) < until
            )
        ),
        key=lambda run: timestamp(run["run_started_at"]),
        reverse=True,
    )
    since = None
    for run in successful:
        attempt_start = timestamp(run["run_started_at"])
        if any(
            step["name"] == COMPLETION_STEP and step["conclusion"] == "success"
            for job in get_jobs(run["id"], run["run_attempt"])
            # Job-level retries also return jobs retained from earlier attempts.
            if job.get("started_at") and timestamp(job["started_at"]) >= attempt_start
            for step in job.get("steps", [])
        ):
            since = attempt_start
            break
    bootstrap = since is None
    if bootstrap:
        first_attempt = (
            current if current["run_attempt"] == 1 else get_attempt(current["id"], 1)
        )
        first_start = min(
            [timestamp(first_attempt["run_started_at"])]
            + [timestamp(run["created_at"]) for run in triage_runs]
        )
        since = first_start - timedelta(days=1)
    failures = []
    for run in runs:
        if (
            run["workflow_id"] == current["workflow_id"]
            or run["head_branch"] != branch
            or run["event"] in {"pull_request", "pull_request_target"}
            or timestamp(run["updated_at"]) < since
            or timestamp(run["created_at"]) >= until
        ):
            continue
        for number in range(1, run["run_attempt"] + 1):
            # Run metadata can change long after completion, without a rerun.
            # Even the latest attempt needs its own timestamps.
            attempt = get_attempt(run["id"], number)
            if attempt["status"] != "completed" or not (
                since <= timestamp(attempt["updated_at"]) < until
            ):
                continue
            jobs = [
                {
                    "id": job["id"],
                    "name": job["name"],
                    "url": job["html_url"],
                    "conclusion": job["conclusion"],
                    "steps": [
                        step["name"]
                        for step in job.get("steps", [])
                        if step["conclusion"] in FAILURES
                    ],
                }
                for job in get_jobs(run["id"], number)
                if job["conclusion"] in FAILURES
            ]
            if jobs or attempt["conclusion"] in FAILURES:
                failures.append(
                    {
                        "key": f"{run['id']}:{number}",
                        "run_id": run["id"],
                        "attempt": number,
                        "workflow": run["name"],
                        "path": run["path"],
                        "sha": attempt["head_sha"],
                        "conclusion": attempt["conclusion"],
                        "updated_at": attempt["updated_at"],
                        "url": f"{run['html_url']}/attempts/{number}",
                        "jobs": jobs,
                    }
                )
    return {
        "since": since.isoformat(),
        "until": until.isoformat(),
        "bootstrap": bootstrap,
        "branch": branch,
        "failures": sorted(
            failures, key=lambda item: (item["updated_at"], item["key"])
        ),
    }


def request_timeout(deadline):
    remaining = deadline - monotonic()
    if remaining <= 0:
        raise TimeoutError("CI collection deadline reached")
    return min(remaining, REQUEST_SECONDS)


def command_error(error):
    status = re.search(rb"HTTP [1-5][0-9]{2}", error.stderr or b"")
    return status.group().decode() if status else f"exit {error.returncode}"


def main(output):
    scan_deadline = monotonic() + SCAN_SECONDS
    repo = os.environ["GITHUB_REPOSITORY"]
    base = f"repos/{repo}/actions"
    current = api(
        f"{base}/runs/{os.environ['GITHUB_RUN_ID']}"
        f"/attempts/{os.environ['GITHUB_RUN_ATTEMPT']}",
        deadline=scan_deadline,
    )
    repository = api(f"repos/{repo}", deadline=scan_deadline)
    branch = repository["default_branch"]
    if current["head_branch"] != branch:
        raise ValueError("CI triage must run on the default branch")
    # Scan all dates so a recent rerun of an old run is still found.
    runs = {
        run["id"]: run
        for run in branch_runs(
            f"{base}/runs",
            branch,
            timestamp(repository["created_at"]),
            timestamp(current["run_started_at"]),
            deadline=scan_deadline,
        )
    }
    manifest = collect(
        runs.values(),
        current,
        branch,
        lambda run_id, attempt: api(
            f"{base}/runs/{run_id}/attempts/{attempt}", deadline=scan_deadline
        ),
        lambda run_id, attempt: pages(
            f"{base}/runs/{run_id}/attempts/{attempt}/jobs",
            "jobs",
            deadline=scan_deadline,
        ),
    )
    request_timeout(scan_deadline)
    output.mkdir(parents=True, exist_ok=True)
    path = output / "failures.json"
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(manifest, indent=2) + "\n")
    temporary.replace(path)
    print(
        f"Found {len(manifest['failures'])} failed attempts "
        f"from {manifest['since']} to {manifest['until']}"
    )


if __name__ == "__main__":
    try:
        main(Path("/tmp/gh-aw/ci-triage"))
    except subprocess.CalledProcessError as error:
        raise SystemExit(
            f"Could not collect CI failures: {error.cmd[-1]} ({command_error(error)})"
        ) from None
