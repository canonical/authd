#!/usr/bin/env python3
"""Refresh, upgrade, check, or summarize the E2E lock under YARF's runtime pins."""

import argparse
import difflib
import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

# uv writes each package as a name/version pair straight after its table header.
PACKAGE_ENTRY = re.compile(
    r'^\[\[package\]\]\nname = "([^"]+)"\nversion = "([^"]+)"', re.MULTILINE
)


def runtime_requirements(project: Path, yarf_manifest: Path) -> list[str]:
    try:
        subprocess.run(
            ["uv", "lock", "--project", str(project), "--check"],
            check=True,
            capture_output=True,
            text=True,
        )
    except subprocess.CalledProcessError as error:
        if project == yarf_manifest.parent:
            hint = (
                "hint: YARF's lock is stale. Update and commit it in YARF, then "
                "update the submodule checkout. "
                "e2e-tests/update_dependencies.py cannot refresh YARF's lock."
            )
        else:
            hint = (
                "hint: To update the E2E lockfile, run "
                "`python3 e2e-tests/update_dependencies.py`."
            )
        error.stderr = error.stderr.replace(
            "hint: To update the lockfile, run `uv lock`.", hint
        )
        raise
    with tempfile.TemporaryDirectory(prefix="authd-yarf-export-") as directory:
        projection = Path(directory)
        shutil.copyfile(yarf_manifest, projection / "pyproject.toml")
        shutil.copyfile(project / "uv.lock", projection / "uv.lock")
        # Select YARF's graph from the checked lock without resolving it again.
        result = subprocess.run(
            [
                "uv",
                "export",
                "--project",
                str(projection),
                "--frozen",
                "--no-default-groups",
                "--no-emit-package",
                "yarf",
                "--no-header",
                "--no-annotate",
                "--no-hashes",
            ],
            check=True,
            capture_output=True,
            text=True,
        )
    return sorted(result.stdout.splitlines())


def locked_python_range(project: Path) -> str:
    # uv normalizes this header and checks it against the project manifest.
    for line in (project / "uv.lock").read_text().splitlines():
        if line.startswith("requires-python = "):
            return json.loads(line.removeprefix("requires-python = "))
    raise ValueError(f"{project / 'uv.lock'} has no requires-python header")


def lock_changes(previous: Path, current: Path) -> list[str]:
    """Return one Markdown bullet per package whose versions differ in two locks."""

    def locked(lock: Path) -> dict[str, list[str]]:
        text = lock.read_text()
        entries = PACKAGE_ENTRY.findall(text)
        if len(entries) != text.count("[[package]]"):
            raise ValueError(f"{lock} has package entries this parser cannot read")
        versions: dict[str, list[str]] = {}
        for name, version in entries:
            versions.setdefault(name, []).append(version)
        return versions

    def described(versions: list[str] | None) -> str:
        return ", ".join(versions) if versions else "not locked"

    before, after = locked(previous), locked(current)
    return [
        f"- `{name}`: {described(before.get(name))} -> {described(after.get(name))}"
        for name in sorted(before.keys() | after.keys())
        if before.get(name) != after.get(name)
    ]


def check_runtime(project: Path, expected: list[str], expected_python: str) -> bool:
    actual = runtime_requirements(project, project / ".yarf" / "pyproject.toml")
    actual_python = locked_python_range(project)
    if actual_python != expected_python:
        print(
            f"E2E requires-python ({actual_python}) differs from YARF "
            f"({expected_python}). Update requires-python in "
            "e2e-tests/pyproject.toml to match YARF before refreshing uv.lock.",
            file=sys.stderr,
        )
        return False
    if actual == expected:
        return True
    print(
        "\n".join(
            difflib.unified_diff(
                expected,
                actual,
                fromfile="YARF runtime",
                tofile="E2E runtime",
                lineterm="",
            )
        ),
        file=sys.stderr,
    )
    print(
        "YARF runtime dependencies differ. Run python3 "
        "e2e-tests/update_dependencies.py after updating the YARF submodule.",
        file=sys.stderr,
    )
    return False


def update_dependencies(project: Path, *, check: bool, upgrade: bool = False) -> bool:
    expected = runtime_requirements(
        project / ".yarf", project / ".yarf" / "pyproject.toml"
    )
    expected_python = locked_python_range(project / ".yarf")
    if check:
        return check_runtime(project, expected, expected_python)

    with tempfile.TemporaryDirectory(prefix="authd-e2e-lock-") as directory:
        temporary = Path(directory)
        candidate = temporary / "candidate"
        (candidate / ".yarf").mkdir(parents=True)
        shutil.copyfile(
            project / ".yarf" / "pyproject.toml",
            candidate / ".yarf" / "pyproject.toml",
        )
        shutil.copyfile(project / "uv.lock", candidate / "uv.lock")
        manifest = (project / "pyproject.toml").read_text()
        (candidate / "pyproject.toml").write_text(
            manifest
            + "\n[tool.uv]\nconstraint-dependencies = "
            + json.dumps(expected)
            + "\n"
        )
        command = ["uv", "lock", "--project", str(candidate)]
        if upgrade:
            command.append("--upgrade")
        subprocess.run(command, check=True)
        # Drop the temporary constraint metadata while keeping the resolved pins.
        (candidate / "pyproject.toml").write_text(manifest)
        subprocess.run(["uv", "lock", "--project", str(candidate)], check=True)
        if not check_runtime(candidate, expected, expected_python):
            return False
        with tempfile.TemporaryDirectory(
            prefix="authd-e2e-lock-", dir=project
        ) as directory:
            replacement = Path(directory) / "uv.lock"
            shutil.copyfile(candidate / "uv.lock", replacement)
            shutil.copymode(project / "uv.lock", replacement)
            replacement.replace(project / "uv.lock")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "--check", action="store_true", help="Check the lock without changing it"
    )
    mode.add_argument(
        "--upgrade",
        action="store_true",
        help="Upgrade E2E dependencies while keeping YARF's runtime pins",
    )
    mode.add_argument(
        "--summary",
        metavar="PREVIOUS_LOCK",
        help="List the uv.lock changes since PREVIOUS_LOCK",
    )
    args = parser.parse_args()
    try:
        if args.summary:
            lock = Path(__file__).parent / "uv.lock"
            print("\n".join(lock_changes(Path(args.summary), lock)) or "- none")
            return 0
        return (
            0
            if update_dependencies(
                Path(__file__).parent, check=args.check, upgrade=args.upgrade
            )
            else 1
        )
    except subprocess.CalledProcessError as error:
        if error.stderr:
            print(error.stderr, file=sys.stderr, end="")
        print(
            "E2E dependency resolution failed; uv.lock was not changed.",
            file=sys.stderr,
        )
        return error.returncode
    except (OSError, ValueError) as error:
        print(f"E2E dependency update failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
