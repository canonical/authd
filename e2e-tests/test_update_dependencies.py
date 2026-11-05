"""Exercise lock updates with local wheels and no registry access."""

import io
import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
import zipfile
from collections.abc import Sequence
from contextlib import redirect_stderr
from pathlib import Path
from unittest.mock import patch

from update_dependencies import runtime_requirements, update_dependencies


class UpdateDependenciesTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        directory = Path(temporary.name)
        self.project = directory / "project"
        self.upstream = self.project / ".yarf"
        self.upstream.mkdir(parents=True)
        wheelhouse = directory / "wheels"
        wheelhouse.mkdir()
        environment = patch.dict(
            os.environ,
            {
                "UV_NO_INDEX": "1",
                "UV_OFFLINE": "1",
                "UV_FIND_LINKS": str(wheelhouse),
                "UV_CACHE_DIR": str(directory / "cache"),
                "UV_PYTHON_DOWNLOADS": "never",
            },
        )
        environment.start()
        self.addCleanup(environment.stop)
        for version in ("1.0", "2.0", "3.0"):
            self.write_wheel(wheelhouse, "runtime", version)
        for version in ("1.0", "2.0"):
            self.write_wheel(wheelhouse, "devtool", version)
            self.write_wheel(
                wheelhouse,
                "authd-extra",
                version,
                requires=(
                    ["runtime>=3; python_version >= '3.12'"] if version == "2.0" else []
                ),
            )
        (self.upstream / "pyproject.toml").write_text(
            '[project]\nname = "yarf"\nversion = "0.0.0"\n'
            'requires-python = ">=3.10,<3.15"\n'
            'dependencies = ["runtime>=1,<4"]\n'
            '[dependency-groups]\ndev = ["devtool>=1,<3"]\n'
        )
        self.runtime_pins = [
            "runtime==1.0; python_version < '3.12'",
            "runtime==2.0; python_version >= '3.12'",
        ]
        self.lock_with_constraints(self.upstream, [*self.runtime_pins, "devtool==1.0"])
        shutil.copyfile(self.upstream / "uv.lock", self.project / "uv.lock")
        (self.project / "pyproject.toml").write_text(
            '[project]\nname = "authd-e2e-tests"\nversion = "0.0.0"\n'
            'requires-python = ">=3.10,<3.15"\n'
            'dependencies = ["yarf", "authd-extra>=1,<3"]\n'
            '[tool.uv.sources]\nyarf = { path = ".yarf" }\n'
        )
        self.lock_with_constraints(
            self.project,
            [*self.runtime_pins, "authd-extra==1.0"],
        )

    def write_wheel(
        self, directory: Path, name: str, version: str, requires: Sequence[str] = ()
    ):
        normalized = name.replace("-", "_")
        info = f"{normalized}-{version}.dist-info"
        with zipfile.ZipFile(
            directory / f"{normalized}-{version}-py3-none-any.whl", "w"
        ) as wheel:
            metadata = (
                f"Metadata-Version: 2.3\nName: {name}\nVersion: {version}\n"
                "Requires-Python: >=3.10\n"
                + "".join(f"Requires-Dist: {requirement}\n" for requirement in requires)
            )
            wheel.writestr(f"{info}/METADATA", metadata)
            wheel.writestr(
                f"{info}/WHEEL",
                "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
            )
            wheel.writestr(f"{info}/RECORD", "")

    def lock(self, project: Path, *options: str):
        subprocess.run(
            ["uv", "lock", "--project", str(project), *options],
            check=True,
            capture_output=True,
            text=True,
        )

    def lock_with_constraints(self, project: Path, constraints: list[str]):
        manifest_path = project / "pyproject.toml"
        manifest = manifest_path.read_text()
        manifest_path.write_text(
            manifest
            + "\n[tool.uv]\nconstraint-dependencies = "
            + json.dumps(constraints)
            + "\n"
        )
        self.lock(project)
        manifest_path.write_text(manifest)
        self.lock(project)

    def test_check_ignores_dev_tools_and_authd_only_dependencies(self):
        before = (self.project / "uv.lock").read_bytes()
        self.assertTrue(update_dependencies(self.project, check=True))
        self.assertEqual((self.project / "uv.lock").read_bytes(), before)
        self.assertNotIn('[[package]]\nname = "devtool"\n', before.decode())

    def test_check_rejects_stale_root_metadata_without_changing_the_lock(self):
        manifest_path = self.project / "pyproject.toml"
        manifest_path.write_text(
            manifest_path.read_text().replace("authd-extra>=1,<3", "authd-extra==1.0")
        )
        before = (self.project / "uv.lock").read_bytes()
        with self.assertRaises(subprocess.CalledProcessError) as failure:
            update_dependencies(self.project, check=True)
        self.assertIn(
            "`python3 e2e-tests/update_dependencies.py`", failure.exception.stderr
        )
        self.assertNotIn("run `uv lock`", failure.exception.stderr)
        self.assertEqual((self.project / "uv.lock").read_bytes(), before)

    def test_stale_yarf_lock_requires_an_upstream_update(self):
        manifest_path = self.upstream / "pyproject.toml"
        manifest_path.write_text(
            manifest_path.read_text().replace("runtime>=1,<4", "runtime>=1,<3")
        )
        paths = [self.project / "uv.lock", self.upstream / "uv.lock"]
        before = [path.read_bytes() for path in paths]
        for check in (True, False):
            with (
                self.subTest(check=check),
                self.assertRaises(subprocess.CalledProcessError) as failure,
            ):
                update_dependencies(self.project, check=check)
            self.assertIn("YARF's lock is stale", failure.exception.stderr)
            self.assertIn("Update and commit it in YARF", failure.exception.stderr)
            self.assertIn("cannot refresh YARF's lock", failure.exception.stderr)
            self.assertNotIn("run `uv lock`", failure.exception.stderr)
            self.assertEqual([path.read_bytes() for path in paths], before)

    def test_python_range_mismatch_does_not_change_the_lock_or_manifests(self):
        manifest_path = self.upstream / "pyproject.toml"
        manifest = manifest_path.read_text()
        for python_range in (
            ">=3.10,<3.14",
            ">=3.10,<3.15,!=3.11.1",
        ):
            manifest_path.write_text(manifest.replace(">=3.10,<3.15", python_range))
            self.lock(self.upstream)
            paths = [
                self.project / "pyproject.toml",
                self.project / "uv.lock",
                manifest_path,
                self.upstream / "uv.lock",
            ]
            before = [path.read_bytes() for path in paths]
            for check, upgrade in ((True, False), (False, False), (False, True)):
                with (
                    self.subTest(
                        python_range=python_range, check=check, upgrade=upgrade
                    ),
                    redirect_stderr(io.StringIO()) as errors,
                ):
                    self.assertFalse(
                        update_dependencies(self.project, check=check, upgrade=upgrade)
                    )
                    self.assertIn("E2E requires-python", errors.getvalue())
                    self.assertIn("differs from YARF", errors.getvalue())
                    self.assertIn("e2e-tests/pyproject.toml", errors.getvalue())
                    self.assertEqual([path.read_bytes() for path in paths], before)

    def test_check_accepts_reordered_python_specifiers(self):
        manifest_path = self.project / "pyproject.toml"
        manifest_path.write_text(
            manifest_path.read_text().replace(">=3.10,<3.15", "<3.15, >=3.10")
        )
        self.lock(self.project)
        self.assertTrue(update_dependencies(self.project, check=True))

    def test_refresh_accepts_updated_root_python_range(self):
        for project in (self.upstream, self.project):
            manifest_path = project / "pyproject.toml"
            manifest_path.write_text(
                manifest_path.read_text().replace(">=3.10,<3.15", ">=3.10,<3.14")
            )
        self.lock(self.upstream)
        self.assertTrue(update_dependencies(self.project, check=False))
        self.assertTrue(update_dependencies(self.project, check=True))

    def test_refresh_restores_runtime_markers_without_upgrading_authd(self):
        self.lock(self.project, "--upgrade-package", "runtime==3.0")
        lock_before = (self.project / "uv.lock").read_bytes()
        upstream_before = (self.upstream / "uv.lock").read_bytes()
        manifest_before = (self.project / "pyproject.toml").read_bytes()
        with redirect_stderr(io.StringIO()) as errors:
            self.assertFalse(update_dependencies(self.project, check=True))
        self.assertIn("runtime==3.0", errors.getvalue())
        self.assertEqual((self.project / "uv.lock").read_bytes(), lock_before)

        self.assertTrue(update_dependencies(self.project, check=False))
        self.assertTrue(update_dependencies(self.project, check=True))
        exported = runtime_requirements(self.project, self.upstream / "pyproject.toml")
        self.assertEqual(
            exported,
            [
                "runtime==1.0 ; python_full_version < '3.12'",
                "runtime==2.0 ; python_full_version >= '3.12'",
            ],
        )
        self.assertEqual((self.upstream / "uv.lock").read_bytes(), upstream_before)
        self.assertEqual(
            (self.project / "pyproject.toml").read_bytes(), manifest_before
        )
        self.assertIn(
            'name = "authd-extra"\nversion = "1.0"',
            (self.project / "uv.lock").read_text(),
        )

    def test_check_rejects_swapped_markers_with_the_same_versions(self):
        self.lock_with_constraints(
            self.project,
            [
                "runtime==2.0; python_version < '3.12'",
                "runtime==1.0; python_version >= '3.12'",
            ],
        )
        with redirect_stderr(io.StringIO()) as errors:
            self.assertFalse(update_dependencies(self.project, check=True))
        self.assertIn("YARF runtime dependencies differ", errors.getvalue())

    def test_upgrade_updates_authd_and_dev_tools_without_changing_yarf_runtime(self):
        self.write_wheel(Path(os.environ["UV_FIND_LINKS"]), "authd-extra", "1.5")
        manifest_path = self.project / "pyproject.toml"
        manifest_path.write_text(
            manifest_path.read_text()
            + '\n[dependency-groups]\ndev = ["devtool>=1,<3"]\n'
        )
        self.lock_with_constraints(
            self.project, [*self.runtime_pins, "authd-extra==1.0", "devtool==1.0"]
        )
        paths = [
            manifest_path,
            self.upstream / "pyproject.toml",
            self.upstream / "uv.lock",
        ]
        before = [path.read_bytes() for path in paths]

        self.assertTrue(update_dependencies(self.project, check=False, upgrade=True))
        self.assertTrue(update_dependencies(self.project, check=True))
        lock = (self.project / "uv.lock").read_text()
        # Version 2.0 conflicts with YARF on newer Python versions.
        self.assertIn('name = "authd-extra"\nversion = "1.5"', lock)
        self.assertIn('name = "devtool"\nversion = "2.0"', lock)
        self.assertEqual([path.read_bytes() for path in paths], before)

    def test_conflict_does_not_change_the_lock_or_manifests(self):
        manifest_path = self.project / "pyproject.toml"
        manifest_path.write_text(
            manifest_path.read_text().replace("authd-extra>=1,<3", "authd-extra==2.0")
        )
        self.lock(self.project)
        paths = [
            manifest_path,
            self.project / "uv.lock",
            self.upstream / "pyproject.toml",
            self.upstream / "uv.lock",
        ]
        before = [path.read_bytes() for path in paths]
        with self.assertRaises(subprocess.CalledProcessError):
            update_dependencies(self.project, check=False)
        self.assertEqual([path.read_bytes() for path in paths], before)

    def test_refresh_preserves_lock_permissions_with_restrictive_umask(self):
        lock = self.project / "uv.lock"
        for mode in (0o644, 0o640, 0o600):
            with self.subTest(mode=oct(mode)):
                lock.chmod(mode)
                previous_umask = os.umask(0o077)
                try:
                    self.assertTrue(update_dependencies(self.project, check=False))
                finally:
                    os.umask(previous_umask)
                self.assertEqual(stat.S_IMODE(lock.stat().st_mode), mode)

    def test_partial_copy_does_not_change_the_lock(self):
        before = (self.project / "uv.lock").read_bytes()
        entries_before = set(self.project.iterdir())
        copyfile = shutil.copyfile

        def partial_copy(source, destination):
            if (
                source.parent.name == "candidate"
                and destination.parent.parent == self.project
            ):
                destination.write_bytes(b"partial lock")
                raise OSError("disk full")
            return copyfile(source, destination)

        with (
            patch("update_dependencies.shutil.copyfile", side_effect=partial_copy),
            self.assertRaisesRegex(OSError, "disk full"),
        ):
            update_dependencies(self.project, check=False)

        self.assertEqual((self.project / "uv.lock").read_bytes(), before)
        self.assertEqual(set(self.project.iterdir()), entries_before)

    def test_failed_replace_does_not_change_the_lock(self):
        before = (self.project / "uv.lock").read_bytes()
        entries_before = set(self.project.iterdir())
        with (
            patch.object(Path, "replace", side_effect=OSError("replace failed")),
            self.assertRaisesRegex(OSError, "replace failed"),
        ):
            update_dependencies(self.project, check=False)

        self.assertEqual((self.project / "uv.lock").read_bytes(), before)
        self.assertEqual(set(self.project.iterdir()), entries_before)


if __name__ == "__main__":
    unittest.main()
