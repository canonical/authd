#!/usr/bin/env python3
"""Create a GitHub-signed bot commit without publishing a branch."""

import argparse
import base64
import json
import os
import re
import subprocess
import sys

BOT_EMAIL = "41898282+github-actions[bot]@users.noreply.github.com"


def git(*args, input=None):
    return subprocess.run(
        ["git", "--no-pager", *args],
        input=input,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=True,
    ).stdout


def api(repository, endpoint, body):
    env = os.environ.copy()
    if endpoint == "trees":
        # GitHub checks workflow-write access when creating trees, not just refs.
        env["GH_TOKEN"] = env["WORKFLOW_UPDATE_TOKEN"]
    env.pop("WORKFLOW_UPDATE_TOKEN", None)
    result = subprocess.run(
        [
            "gh",
            "api",
            "--hostname",
            "github.com",
            "--method",
            "POST",
            f"repos/{repository}/git/{endpoint}",
            "--input",
            "-",
        ],
        input=json.dumps(body).encode(),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        check=False,
        timeout=60,
    )
    if result.returncode:
        status = re.search(rb"\(HTTP ([0-9]{3})\)", result.stderr)
        detail = f" (HTTP {status[1].decode()})" if status else ""
        raise RuntimeError(f"GitHub Git {endpoint} request failed{detail}")
    return json.loads(result.stdout)


def sign_update(repository, message):
    parent = git("rev-parse", "HEAD").decode().strip()
    tree = git("write-tree").decode().strip()
    changes = git(
        "diff", "--cached", "--raw", "--no-abbrev", "--no-renames", "-z", "HEAD"
    ).split(b"\0")
    if changes == [b""]:
        return parent

    entries = []
    for raw, path in zip(changes[0:-1:2], changes[1:-1:2]):
        old_mode, mode, _, sha, status = raw.decode().split()
        deleted = status == "D"
        object_type = (
            "commit" if (old_mode[1:] if deleted else mode) == "160000" else "blob"
        )
        if not deleted and object_type == "blob":
            blob = api(
                repository,
                "blobs",
                {
                    "content": base64.b64encode(git("cat-file", "blob", sha)).decode(),
                    "encoding": "base64",
                },
            )
            if blob["sha"] != sha:
                raise RuntimeError("GitHub returned a different blob")
        entries.append(
            {
                "path": path.decode(),
                "mode": old_mode[1:] if deleted else mode,
                "type": object_type,
                "sha": None if deleted else sha,
            }
        )

    remote_tree = api(
        repository,
        "trees",
        {
            "base_tree": git("rev-parse", "HEAD^{tree}").decode().strip(),
            "tree": entries,
        },
    )
    if remote_tree["sha"] != tree:
        raise RuntimeError("GitHub returned a different tree")

    # Custom author, committer, or signature fields disable GitHub's bot signing.
    commit = api(
        repository, "commits", {"parents": [parent], "tree": tree, "message": message}
    )
    verification = commit["verification"]
    if not verification["verified"] or commit["author"]["email"] != BOT_EMAIL:
        raise RuntimeError(
            "GitHub did not create a verified github-actions[bot] commit"
        )

    headers, body = verification["payload"].split("\n\n", 1)
    signature = verification["signature"].replace("\n", "\n ")
    signed = f"{headers}\ngpgsig {signature}\n\n{body}".encode()
    sha = (
        git("hash-object", "-t", "commit", "-w", "--stdin", input=signed)
        .decode()
        .strip()
    )
    if sha != commit["sha"]:
        raise RuntimeError("The signed commit does not match GitHub's commit")
    if (
        git("rev-parse", f"{sha}^").decode().strip() != parent
        or git("rev-parse", f"{sha}^{{tree}}").decode().strip() != tree
    ):
        raise RuntimeError("The signed commit does not match the staged update")

    # Keep the signed object unchanged; the PR action publishes it with the PAT.
    git("update-ref", "HEAD", sha, parent)
    return sha


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--message", required=True)
    args = parser.parse_args()
    try:
        sha = sign_update(os.environ["GITHUB_REPOSITORY"], args.message)
    except RuntimeError as error:
        print(f"{error}; no branch was published", file=sys.stderr)
        return 1
    except (KeyError, ValueError, subprocess.SubprocessError):
        print(
            "Could not create a verified bot commit; no branch was published",
            file=sys.stderr,
        )
        return 1
    print(f"Verified bot commit: {sha}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
