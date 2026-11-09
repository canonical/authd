#!/usr/bin/env python3
"""Collect completed CI failures without missing old runs that were rerun."""

import json
import os
import re
import resource
import shutil
import stat
import subprocess
import tempfile
import zipfile
import zlib
from bisect import bisect_right
from datetime import datetime, timedelta
from pathlib import Path
from time import monotonic
from urllib.parse import urlencode
from xml.parsers import expat

FAILURES = {"failure", "timed_out", "action_required", "startup_failure", "stale"}
COMPLETION_STEP = "Require complete triage before advancing the checkpoint"
SCAN_SECONDS = 12 * 60
EVIDENCE_RESERVE_SECONDS = 3 * 60
ARTIFACT_RESERVE_SECONDS = 60
REQUEST_SECONDS = 60
ARCHIVE_REQUEST_SECONDS = 120
PAGE_SIZE = 100
FILTERED_RUN_LIMIT = 1000
MAX_LOG_BYTES = 16 * 1024**2
MAX_ARCHIVE_BYTES = 256 * 1024**2
MAX_DIAGNOSTIC_BYTES = 32 * 1024**2
MAX_ROBOT_RESULTS_BYTES = 32 * 1024**2
MAX_ARCHIVE_MEMBERS = 10_000
MAX_ARCHIVE_FILES = 1_000
MAX_DOWNLOAD_BYTES = 2 * 1024**3
MAX_STAGED_BYTES = 2 * 1024**3

# shortcut: match workflow display names until the jobs API exposes job keys.
QA_ARTIFACT_JOBS = {
    "Go Tests with Coverage Collection": "go-tests-coverage",
    "Retry Go Tests with Coverage Collection": "go-tests-coverage-retry",
    "Go Tests with Race Detector": "go-tests-race",
    "Go PAM tests with Address Sanitizer": "go-tests-asan",
}
E2E_WORKFLOW = ".github/workflows/e2e-tests.yaml"
E2E_JOB = re.compile(
    r"provision-and-run \(([\w.-]+)\) / e2e-test \(([\w.-]+)\) / Run e2e-tests \(\2\)",
    re.ASCII,
)
# shortcut: read the listener's text until failed suites have a small manifest.
E2E_FAILED_SUITE = re.compile(r"^(?:\d{4}-\S+Z )?Suite  (.+)  FAIL  ")
DIAGNOSTIC_SUFFIXES = {
    ".html",
    ".journal",
    ".json",
    ".log",
    ".stderr",
    ".stdout",
    ".txt",
    ".xml",
}


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
        attempt_starts = []
        for number in range(1, run["run_attempt"] + 1):
            # Run metadata can change long after completion, without a rerun.
            # Even the latest attempt needs its own timestamps.
            attempt = get_attempt(run["id"], number)
            attempt_starts.append(timestamp(attempt["run_started_at"]))
            if attempt["status"] != "completed" or not (
                since <= timestamp(attempt["updated_at"]) < until
            ):
                continue
            jobs = []
            for job in get_jobs(run["id"], number):
                if job["conclusion"] not in FAILURES:
                    continue
                # GitHub labels retained jobs with the requested attempt, not
                # the one that ran the job and uploaded its artifact.
                started_at = job.get("started_at")
                job_attempt = (
                    max(1, bisect_right(attempt_starts, timestamp(started_at)))
                    if started_at
                    else number
                )
                jobs.append(
                    {
                        "id": job["id"],
                        "name": job["name"],
                        "url": job["html_url"],
                        "conclusion": job["conclusion"],
                        "attempt": job_attempt,
                        "steps": [
                            step["name"]
                            for step in job.get("steps", [])
                            if step["conclusion"] in FAILURES
                        ],
                    }
                )
            if jobs or attempt["conclusion"] in FAILURES:
                failures.append(
                    {
                        "key": f"{run['id']}:{number}",
                        "run_id": run["id"],
                        "attempt": number,
                        "workflow": run["name"],
                        # GitHub may append @ref to the workflow path.
                        "path": run["path"].partition("@")[0],
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


def request_timeout(deadline, limit=REQUEST_SECONDS):
    remaining = deadline - monotonic()
    if remaining <= 0:
        raise TimeoutError("CI collection deadline reached")
    return min(remaining, limit)


def command_error(error):
    if isinstance(error, subprocess.CalledProcessError):
        status = re.search(rb"HTTP [1-5][0-9]{2}", error.stderr or b"")
        return status.group().decode() if status else f"exit {error.returncode}"
    if isinstance(error, TimeoutError):
        return "evidence deadline reached"
    if isinstance(error, subprocess.TimeoutExpired):
        return "request timed out"
    if isinstance(error, ValueError):
        return str(error)
    return type(error).__name__


def expected_artifact(failure, job):
    path, name, attempt = failure["path"], job["name"], job["attempt"]
    if path == E2E_WORKFLOW:
        if match := E2E_JOB.fullmatch(name):
            release, broker = match.groups()
            return f"e2e-tests-{release}-{broker}-{failure['run_id']}-{attempt}"
    if path == ".github/workflows/qa.yaml" and name in QA_ARTIFACT_JOBS:
        return f"authd-{QA_ARTIFACT_JOBS[name]}-artifacts-{attempt}"
    return None


def robot_failed_suites(archive, deadline):
    try:
        entry = archive.getinfo("output.xml")
    except KeyError:
        return set()
    # shortcut: skip oversized results; add a compact suite summary if needed.
    if (
        entry.file_size > MAX_ROBOT_RESULTS_BYTES
        or entry.compress_type not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED)
        or stat.S_IFMT(entry.external_attr >> 16) not in (0, stat.S_IFREG)
    ):
        return set()

    suites = set()
    parents = []
    depth = 0

    def start(tag, attrs):
        nonlocal depth
        if depth < 3:
            parents.append(tag)
        elif (
            depth == 3
            and tag == "stat"
            and parents == ["robot", "statistics", "suite"]
            and "name" in attrs
            and int(attrs.get("fail", "0")) > 0
        ):
            suites.add(attrs["name"].rsplit(".", 1)[-1])
        depth += 1

    def end(_tag):
        nonlocal depth
        depth -= 1
        if depth < 3:
            parents.pop()

    def reject_doctype(*_args):
        raise ValueError("Robot results must not contain a DOCTYPE")

    parser = expat.ParserCreate()
    parser.StartElementHandler = start
    parser.EndElementHandler = end
    parser.StartDoctypeDeclHandler = reject_doctype
    try:
        with archive.open(entry) as source:
            while True:
                request_timeout(deadline)
                chunk = source.read(64 * 1024)
                parser.Parse(chunk, not chunk)
                if not chunk:
                    return suites
    except (EOFError, expat.ExpatError, ValueError, zipfile.BadZipFile, zlib.error):
        return set()


def extract_diagnostics(raw, directory, deadline, limit, *, failed_suites=None):
    def e2e_priority(entry):
        name = entry.filename
        if name == "log.html":
            return 0
        if name.endswith(".journal"):
            suite, separator, _ = name.partition("/journal/")
            if separator and suite in failed_suites:
                return 1
        if name.endswith(".webp"):
            suite, separator, _ = name.partition("/screenshots/")
            if separator and suite in failed_suites:
                return 2
        return 3 if name in ("report.html", "output.xml") else 4

    remaining = limit
    limit_hit = None
    staged_files = 0
    with zipfile.ZipFile(raw) as archive:
        entries = archive.infolist()
        # shortcut: ZipFile parses the central directory before this cap; the
        # 256 MiB download cap bounds that work. Use a streaming parser if
        # that cap grows.
        if len(entries) > MAX_ARCHIVE_MEMBERS:
            raise ValueError(f"archive exceeds the {MAX_ARCHIVE_MEMBERS}-member limit")
        if failed_suites is not None:
            failed_suites.update(robot_failed_suites(archive, deadline))
            entries = sorted(entries, key=e2e_priority)
        for entry in entries:
            request_timeout(deadline)
            path = Path(entry.filename)
            if path.is_absolute() or ".." in path.parts or "\\" in entry.filename:
                raise zipfile.BadZipFile
            if entry.is_dir():
                continue
            if stat.S_IFMT(entry.external_attr >> 16) not in (0, stat.S_IFREG):
                raise zipfile.BadZipFile
            if (
                path.suffix not in DIAGNOSTIC_SUFFIXES
                and not (
                    path.suffix[1:].isdigit() and path.with_suffix("").suffix == ".log"
                )
                and not (
                    failed_suites is not None
                    and len(path.parts) >= 3
                    and path.parts[1] == "screenshots"
                    and path.suffix == ".webp"
                )
            ):
                continue
            if entry.file_size > remaining:
                limit_hit = limit_hit or "diagnostic size limit"
                continue
            if entry.compress_type not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
                raise zipfile.BadZipFile
            if staged_files >= MAX_ARCHIVE_FILES:
                limit_hit = f"{MAX_ARCHIVE_FILES}-file staging limit"
                continue
            destination = directory / path
            destination.parent.mkdir(parents=True, exist_ok=True)
            with archive.open(entry) as source, destination.open("xb") as output:
                shutil.copyfileobj(source, output)
            staged_files += 1
            remaining -= entry.file_size
    return limit - remaining, limit_hit


def collect_evidence(repo, failures, output: Path, deadline):
    output = output.resolve()
    download_bytes_left = MAX_DOWNLOAD_BYTES
    staged_bytes_left = MAX_STAGED_BYTES
    # Logs run first; keep a slice for artifacts, but never take more than
    # half of a short evidence window.
    evidence_remaining = max(0.0, deadline - monotonic())
    log_deadline = deadline - min(ARTIFACT_RESERVE_SECONDS, evidence_remaining / 2)

    def download(
        endpoint, raw, max_bytes, *, request_seconds=REQUEST_SECONDS, deadline
    ):
        nonlocal download_bytes_left
        limit = min(download_bytes_left, max_bytes)
        if not limit:
            return "download byte budget exhausted"
        try:
            subprocess.run(
                ["gh", "api", "--allow-escape-sequences", endpoint],
                stdout=raw,
                stderr=subprocess.PIPE,
                check=True,
                timeout=request_timeout(deadline, request_seconds),
                preexec_fn=lambda: resource.setrlimit(
                    resource.RLIMIT_FSIZE, (limit, limit)
                ),
            )
        except (OSError, subprocess.SubprocessError) as error:
            return (
                "download size limit exceeded"
                if raw.tell() >= limit
                else command_error(error)
            )
        finally:
            download_bytes_left -= raw.tell()
        raw.seek(0)
        return None

    artifacts = []
    for failure in failures:
        run_id = failure["run_id"]
        for job in failure["jobs"]:
            name = expected_artifact(failure, job)
            failed_suites = set() if name and failure["path"] == E2E_WORKFLOW else None
            if name:
                artifacts.append((run_id, job, name, failed_suites))
            path = (
                output / "logs" / f"{run_id}-{failure['attempt']}" / f"{job['id']}.log"
            )
            try:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.unlink(missing_ok=True)
                with (
                    tempfile.TemporaryFile() as raw,
                    tempfile.TemporaryDirectory(dir=path.parent) as temporary,
                ):
                    error = download(
                        f"repos/{repo}/actions/jobs/{job['id']}/logs",
                        raw,
                        MAX_LOG_BYTES,
                        deadline=log_deadline,
                    )
                    if error:
                        job["log_error"] = f"Job log unavailable ({error})"
                        continue
                    cleaned = Path(temporary) / "log"
                    with cleaned.open("w", encoding="utf-8") as destination:
                        for line in raw:
                            text = line.decode("utf-8", errors="replace")
                            text = re.sub(
                                r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[ -/]*[@-~])",
                                "",
                                text,
                            )
                            text = re.sub(r"[\x00-\x08\x0b-\x1f\x7f-\x9f]", "", text)
                            destination.write(text)
                            if failed_suites is not None and (
                                match := E2E_FAILED_SUITE.match(text)
                            ):
                                failed_suites.add(match[1])
                    size = cleaned.stat().st_size
                    if size > staged_bytes_left:
                        job["log_error"] = (
                            "Job log exceeds the staged evidence size budget"
                        )
                        continue
                    cleaned.replace(path)
                    staged_bytes_left -= size
                job["log_path"] = str(path)
            except OSError as error:
                job["log_error"] = f"Job log unavailable ({command_error(error)})"

    # Give jobs with no staged log the first chance at a second source.
    artifacts.sort(key=lambda item: "log_path" in item[1])

    catalogs = {}
    staged = {}
    for run_id, job, name, failed_suites in artifacts:
        if run_id not in catalogs:
            try:
                catalogs[run_id] = {
                    artifact["name"]: artifact
                    for artifact in pages(
                        f"repos/{repo}/actions/runs/{run_id}/artifacts",
                        "artifacts",
                        deadline=deadline,
                    )
                }
            except (OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
                catalogs[run_id] = f"Could not list artifacts ({command_error(error)})"
        catalog = catalogs[run_id]
        if isinstance(catalog, str):
            job["artifact_error"] = catalog
            continue
        artifact = catalog.get(name)
        if not artifact:
            job["artifact_error"] = f"Expected artifact {name} was not found"
            continue
        key = (run_id, artifact["id"])
        if key in staged:
            job.update(staged[key])
            continue
        if artifact.get("expired"):
            job["artifact_error"] = f"Artifact {name} has expired"
            continue
        if not staged_bytes_left:
            job["artifact_error"] = (
                f"Artifact {name} exceeds the diagnostic size budget"
            )
            continue
        size_limit = min(MAX_ARCHIVE_BYTES, download_bytes_left)
        if not 0 < artifact["size_in_bytes"] <= size_limit:
            job["artifact_error"] = f"Artifact {name} exceeds the download size budget"
            continue
        destination = output / "artifacts" / str(run_id) / name
        try:
            destination.parent.mkdir(parents=True, exist_ok=True)
            with (
                tempfile.TemporaryFile() as raw,
                tempfile.TemporaryDirectory(dir=destination.parent) as temporary,
            ):
                error = download(
                    f"repos/{repo}/actions/artifacts/{artifact['id']}/zip",
                    raw,
                    size_limit,
                    request_seconds=ARCHIVE_REQUEST_SECONDS,
                    deadline=deadline,
                )
                if error:
                    job["artifact_error"] = f"Artifact {name} unavailable ({error})"
                    continue
                directory = Path(temporary)
                size, limit_hit = extract_diagnostics(
                    raw,
                    directory,
                    deadline,
                    min(MAX_DIAGNOSTIC_BYTES, staged_bytes_left),
                    failed_suites=failed_suites,
                )
                if not any(directory.iterdir()):
                    job["artifact_error"] = (
                        f"Artifact {name} exceeds the {limit_hit}"
                        if limit_hit
                        else f"Artifact {name} has no usable diagnostics"
                    )
                    continue
                directory.rename(destination)
                staged_bytes_left -= size
        except (
            OSError,
            ValueError,
            zipfile.BadZipFile,
            zlib.error,
            RuntimeError,
        ) as error:
            job["artifact_error"] = (
                f"Artifact {name} unavailable ({command_error(error)})"
            )
            continue
        result = {"artifact_path": str(destination)}
        if limit_hit:
            result["artifact_error"] = (
                f"Some files in {name} were skipped ({limit_hit})"
            )
        staged[key] = result
        job.update(result)


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
    collect_evidence(
        repo, manifest["failures"], output, scan_deadline + EVIDENCE_RESERVE_SECONDS
    )
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
    except (OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
        raise SystemExit(
            f"Could not collect CI failures ({command_error(error)})"
        ) from None
