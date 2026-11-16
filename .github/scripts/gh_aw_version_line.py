#!/usr/bin/env python3
"""Print the gh-aw version line for the updater pull request description."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path


def parse_version(metadata: str) -> str:
    """Return the compiler version from a lock metadata line."""
    _, _, payload = metadata.partition("gh-aw-metadata:")
    try:
        return json.loads(payload).get("compiler_version", "")
    except json.JSONDecodeError:
        return ""


def old_version(lock: Path) -> str:
    """Return the compiler version recorded at HEAD, or an empty string."""
    try:
        content = subprocess.run(
            ["git", "show", f"HEAD:{lock}"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
    except subprocess.CalledProcessError:
        return ""
    first_line = content.splitlines()[0] if content else ""
    return parse_version(first_line)


def version_line(old_versions: list[str], new_version: str) -> str:
    """Return one line per distinct old-to-new pair, or the current version."""
    pairs = sorted(
        {
            f"{old} to {new_version}"
            for old in old_versions
            if old and old != new_version
        }
    )
    if pairs:
        return "\n".join(f"gh-aw: {pair}" for pair in pairs)
    return f"gh-aw version: {new_version}"


def main() -> None:
    new_version = subprocess.run(
        ["gh", "aw", "--version"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.split()[-1]
    locks = sorted(Path(".github/workflows").glob("*.lock.yml"))
    print(version_line([old_version(lock) for lock in locks], new_version))


if __name__ == "__main__":
    main()
