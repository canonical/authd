import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from gh_aw_version_line import old_version, parse_version, version_line


class GhAwVersionLineTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cwd = os.getcwd()
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, self.cwd)
        self.git("init", "--quiet", "--template=")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.com")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "core.hooksPath", "/dev/null")

    def git(self, *args):
        return subprocess.check_output(["git", "--no-pager", *args])

    def write_lock(self, name, version):
        lock = Path(".github/workflows") / name
        lock.parent.mkdir(parents=True, exist_ok=True)
        lock.write_text(f'# gh-aw-metadata: {{"compiler_version":"{version}"}}\n')
        return lock

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "base")

    def test_parses_the_lock_metadata(self):
        self.assertEqual(
            parse_version('# gh-aw-metadata: {"compiler_version":"v1.2.2"}'),
            "v1.2.2",
        )

    def test_parses_the_lock_metadata_with_spaces(self):
        self.assertEqual(
            parse_version('# gh-aw-metadata: {"compiler_version": "v1.2.2"}'),
            "v1.2.2",
        )

    def test_missing_metadata_has_no_version(self):
        self.assertEqual(parse_version("# no metadata"), "")

    def test_reads_the_old_version_from_head(self):
        lock = self.write_lock("a.lock.yml", "v1.0.1")
        self.commit()
        lock.write_text('# gh-aw-metadata: {"compiler_version":"v1.2.2"}\n')
        self.assertEqual(old_version(lock), "v1.0.1")

    def test_a_lock_missing_at_head_has_no_version(self):
        self.write_lock("a.lock.yml", "v1.2.2")
        self.commit()
        self.assertEqual(old_version(Path(".github/workflows/new.lock.yml")), "")

    def test_prints_each_distinct_old_to_new_pair(self):
        self.assertEqual(
            version_line(["v1.0.1", "v0.89.21", "v1.0.1"], "v1.2.2"),
            "gh-aw: v0.89.21 to v1.2.2\ngh-aw: v1.0.1 to v1.2.2",
        )

    def test_prints_the_current_version_when_nothing_moved(self):
        self.assertEqual(
            version_line(["v1.2.2", ""], "v1.2.2"),
            "gh-aw version: v1.2.2",
        )


if __name__ == "__main__":
    unittest.main()
