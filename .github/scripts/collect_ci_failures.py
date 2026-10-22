#!/usr/bin/env python3
"""Collect completed CI failures without missing old runs that were rerun."""

import json
import os
import subprocess
from datetime import datetime, timedelta
from pathlib import Path

FAILURES = {"failure", "timed_out", "action_required", "startup_failure", "stale"}
COMPLETION_STEP = "Require complete triage before advancing the checkpoint"


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def api(endpoint):
    return json.loads(subprocess.check_output(["gh", "api", endpoint], text=True))


def pages(endpoint, key):
    page = 1
    while True:
        items = api(f"{endpoint}?per_page=100&page={page}")[key]
        yield from items
        if len(items) < 100:
            return
        page += 1


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
            if run["conclusion"] == "success" and timestamp(run["updated_at"]) < until
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
            attempt = (
                run if number == run["run_attempt"] else get_attempt(run["id"], number)
            )
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


def main():
    repo = os.environ["GITHUB_REPOSITORY"]
    base = f"repos/{repo}/actions"
    current = api(
        f"{base}/runs/{os.environ['GITHUB_RUN_ID']}"
        f"/attempts/{os.environ['GITHUB_RUN_ATTEMPT']}"
    )
    branch = api(f"repos/{repo}")["default_branch"]
    if current["head_branch"] != branch:
        raise ValueError("CI triage must run on the default branch")
    # Creation-date filters miss reruns of old runs. Unfiltered pagination also
    # avoids the REST API's 1,000-result limit for filtered run searches.
    runs = {run["id"]: run for run in pages(f"{base}/runs", "workflow_runs")}
    manifest = collect(
        runs.values(),
        current,
        branch,
        lambda run_id, attempt: api(f"{base}/runs/{run_id}/attempts/{attempt}"),
        lambda run_id, attempt: pages(
            f"{base}/runs/{run_id}/attempts/{attempt}/jobs", "jobs"
        ),
    )
    output = Path("/tmp/gh-aw/ci-triage")
    output.mkdir(parents=True, exist_ok=True)
    (output / "failures.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(
        f"Found {len(manifest['failures'])} failed attempts "
        f"from {manifest['since']} to {manifest['until']}"
    )


if __name__ == "__main__":
    main()
