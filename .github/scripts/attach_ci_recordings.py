#!/usr/bin/env python3
"""Attach requested recordings before marking triage complete."""

import hashlib
import json
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path
from urllib.parse import urlparse

MAX_RECORDINGS = 40
MAX_VIDEO_BYTES = 100_000_000
MAX_BODY_CHARS = 60_000


def gh(*args):
    return subprocess.check_output(
        ["gh", *args], text=True, stderr=subprocess.STDOUT, timeout=180
    )


def report_error(message):
    message = str(message).replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    print(f"::error::{message}")


def issue_reference(value):
    if isinstance(value, bool):
        raise ValueError("Invalid issue reference")
    value = str(value).lstrip("#").lower()
    if re.fullmatch(r"[1-9][0-9]*|aw_[a-z0-9_]{3,12}", value):
        return value
    raise ValueError("Invalid issue reference")


def published_issues(items, outcomes, repo):
    issues = {}
    for outcome in outcomes:
        index = outcome.get("messageIndex")
        if (
            outcome.get("status") != "success"
            or outcome.get("staged")
            or not isinstance(index, int)
            or isinstance(index, bool)
            or not 0 <= index < len(items)
        ):
            continue
        item = items[index]
        kind = item.get("type")
        if kind not in {"create_issue", "update_issue"} or outcome.get("type") != kind:
            continue
        url = urlparse(outcome.get("url", ""))
        match = re.fullmatch(rf"/{re.escape(repo)}/issues/([1-9][0-9]*)/?", url.path)
        if url.scheme != "https" or url.netloc != "github.com" or not match:
            continue
        reference = (
            item.get("temporary_id")
            if kind == "create_issue"
            else item.get("issue_number")
        )
        issues[issue_reference(reference)] = int(match[1])
    return issues


def recording_path(root, name):
    path = Path(name)
    if (
        path.is_absolute()
        or ".." in path.parts
        or "#" in name
        or path.suffix.lower() != ".mp4"
    ):
        raise ValueError("Recording must be a relative MP4 path inside its artifact")
    resolved = (root / path).resolve()
    if not resolved.is_relative_to(root.resolve()) or not resolved.is_file():
        raise ValueError("Recording is missing or outside its artifact")
    if not 0 < resolved.stat().st_size <= MAX_VIDEO_BYTES:
        raise ValueError("Recording is empty or exceeds the 100 MB upload limit")
    return resolved


def attach_recordings(manifest, items, outcomes, requests, repo, root):
    issues = published_issues(items, outcomes, repo)
    failures = {failure["key"]: failure for failure in manifest["failures"]}
    if not isinstance(requests, list):
        raise ValueError("Recording requests must be a list")
    if len(requests) > MAX_RECORDINGS:
        raise ValueError(f"At most {MAX_RECORDINGS} recording requests are allowed")
    downloads = {}
    failed = 0
    for request in requests:
        try:
            issue = issues[issue_reference(request["issue"])]
            failure = failures[request["failure_key"]]
            artifact = request["artifact"]
            if not re.fullmatch(
                rf"e2e-tests-[a-z0-9-]+-{failure['run_id']}-{failure['attempt']}",
                artifact,
            ):
                raise ValueError("Artifact does not match the failed run and attempt")
            identity = json.dumps(
                [repo, failure["key"], artifact, request["path"]],
                separators=(",", ":"),
            )
            digest = hashlib.sha256(identity.encode()).hexdigest()
            marker = f"<!-- ci-triage-recording:{digest} -->"
            attached = re.compile(
                re.escape(marker)
                + r"\s+https://github\.com/user-attachments/assets/[a-zA-Z0-9-]+(?=\s|$)"
            )
            body = json.loads(
                gh("issue", "view", str(issue), "--repo", repo, "--json", "body")
            )["body"]
            if attached.search(body):
                continue
            if len(body) + len(marker) + 200 > MAX_BODY_CHARS:
                raise ValueError("Issue body has no room for another attachment")
            download_key = (failure["run_id"], artifact)
            if download_key not in downloads:
                directory = root / f"artifact-{len(downloads)}"
                gh(
                    "run",
                    "download",
                    str(failure["run_id"]),
                    "--repo",
                    repo,
                    "--name",
                    artifact,
                    "--dir",
                    str(directory),
                )
                downloads[download_key] = directory
            video = recording_path(downloads[download_key], request["path"])
            upload = root / f"{digest}.mp4"
            shutil.copyfile(video, upload)
            body = json.loads(
                gh("issue", "view", str(issue), "--repo", repo, "--json", "body")
            )["body"]
            if attached.search(body):
                continue
            if len(body) + len(marker) + 200 > MAX_BODY_CHARS:
                raise ValueError("Issue body has no room for another attachment")
            body_file = root / "issue-body.txt"
            body_file.write_text(f"{body}\n\n{marker}\n")
            # One attachment per call makes partial upload results unambiguous.
            gh(
                "issue",
                "edit",
                str(issue),
                "--repo",
                repo,
                "--body-file",
                str(body_file),
                "--attach",
                str(upload),
            )
            updated = json.loads(
                gh("issue", "view", str(issue), "--repo", repo, "--json", "body")
            )["body"]
            if (
                not attached.search(updated)
                or updated.count("https://github.com/user-attachments/assets/")
                <= body.count("https://github.com/user-attachments/assets/")
                or str(upload) in updated
            ):
                raise ValueError("Uploaded recording could not be verified")
            print(f"Attached recording from {failure['key']} to issue #{issue}")
        except (
            KeyError,
            TypeError,
            ValueError,
            OSError,
            subprocess.SubprocessError,
        ) as error:
            detail = (
                error.output
                if isinstance(error, subprocess.CalledProcessError)
                else str(error)
            )
            report_error(f"Recording upload failed. {detail}")
            failed += 1
    if failed:
        raise ValueError(f"{failed} recording uploads failed; triage is incomplete")


def main():
    root = Path(os.environ["CI_TRIAGE_DOWNLOADS"])
    requests_file = root / "manifests/recordings.json"
    with tempfile.TemporaryDirectory() as directory:
        attach_recordings(
            json.loads((root / "manifests/failures.json").read_text()),
            json.loads((root / "agent/agent_output.json").read_text())["items"],
            [
                json.loads(line)
                for line in (root / "safe-outputs/safe-output-outcomes.jsonl")
                .read_text()
                .splitlines()
                if line.strip()
            ],
            json.loads(requests_file.read_text()),
            os.environ["GITHUB_REPOSITORY"],
            Path(directory),
        )


if __name__ == "__main__":
    main()
