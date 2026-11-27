"""Run with python3 .workshop/authd/tests/test_broker_config.py (no sudo)."""

import hashlib
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HELPER = Path(__file__).resolve().parents[1] / "lib" / "broker_config.py"
TEMPLATES = HELPER.parents[3] / "brokers" / "conf" / "variants"


class BrokerConfigTest(unittest.TestCase):
    def command(self, *args, data=b"", ok=True):
        result = subprocess.run(
            [sys.executable, HELPER, *args],
            input=data,
            capture_output=True,
            check=False,
        )
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr.decode())
        else:
            self.assertNotEqual(result.returncode, 0)
        return result.stdout

    def test_seed_and_partial_update_are_atomic(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "broker.conf"
            template = TEMPLATES / "google" / "broker.conf"

            def configure(*fields, ok=True):
                data = b"".join(
                    key.encode() + b"\0" + value.encode() + b"\0"
                    for key, value in fields
                )
                return self.command(
                    "configure", "google", path, template, data=data, ok=ok
                )

            configure(("oidc.client_id", "id"), ok=False)
            self.assertFalse(path.exists())
            self.assertEqual(configure(), b"0 0\n")
            self.assertIn(b"\nssh_allowed_suffixes_first_auth = \n", path.read_bytes())
            # Test-only value exercises literal INI comment characters.
            secret = "dummy#secret;value<part"
            self.assertEqual(
                configure(("oidc.client_id", "old-id"), ("oidc.client_secret", secret)),
                b"0 1\n",
            )
            old = path.read_bytes()
            self.assertIn(secret.encode(), old)
            with path.open("rb") as original:
                self.assertEqual(configure(("oidc.client_id", "new-id")), b"1 1\n")
                self.assertEqual(original.read(), old)
            updated = path.read_bytes()
            self.assertEqual(
                updated, old.replace(b"client_id = old-id", b"client_id = new-id")
            )
            for invalid in ("", "line\nmore", "line\rmore"):
                configure(
                    ("oidc.client_secret", invalid),
                    ("oidc.client_id", "discard-me"),
                    ok=False,
                )
                self.assertEqual(path.read_bytes(), updated)
            digest = hashlib.sha256(updated).hexdigest()
            self.command(
                "edit",
                "google",
                path,
                digest,
                data=updated.replace(secret.encode(), b""),
                ok=False,
            )
            self.assertEqual(path.read_bytes(), updated)
            self.assertEqual(
                self.command("edit", "google", path, digest, data=old), b"updated\n"
            )
            backups = sorted(path.parent.glob("broker.conf.bak-*"))
            self.assertEqual(len(backups), 3)
            self.assertIn(updated, [backup.read_bytes() for backup in backups])
            for file in [path, *backups]:
                self.assertEqual(stat.S_IMODE(file.stat().st_mode), 0o600)

    def test_section_and_drop_in_precedence(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "broker.conf"
            path.write_bytes(
                b"[oidc]\r\nissuer: https://issuer.invalid\r\n"
                b"[flows]\r\n# unset on purpose\r\n"
                b"[users]\r\nowner = preserved@example.invalid\r\n"
            )
            drop_in = path.with_suffix(".conf.d")
            drop_in.mkdir()
            (drop_in / "20-client.conf").write_text("[oidc]\nclient_id = a-client\n")
            args = (
                "configure",
                "msentraid",
                path,
                TEMPLATES / "msentraid" / "broker.conf",
            )
            self.assertEqual(
                self.command(*args, data=b"flows.entra_auth\0true\0"), b"1 1\n"
            )
            updated = path.read_bytes()
            self.assertIn(b"[flows]\r\nentra_auth = true\r\n", updated)
            self.assertIn(b"[users]\r\nowner = preserved@example.invalid\r\n", updated)
            self.command(*args, data=b"oidc.client_id\0ignored-client\0", ok=False)
            self.assertEqual(path.read_bytes(), updated)

    def test_secret_file_only_removes_one_line_ending(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "secret"
            path.write_bytes(b"test-secret\r\n")
            self.assertEqual(self.command("secret-file", path), b"test-secret")
            for invalid in (b"", b"\r\n", b"test-secret\r\n\r\n", b"test\rsecret"):
                path.write_bytes(invalid)
                self.assertEqual(self.command("secret-file", path, ok=False), b"")

    def test_staged_tree_exchange_keeps_previous_tree_until_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            staged, installed = (Path(tmp) / name for name in ("stage", "config"))
            staged.mkdir()
            (staged / "new.conf").write_text("new")
            self.command("replace-tree", staged, installed)
            self.assertFalse(staged.exists())
            staged.mkdir()
            (staged / "replacement.conf").write_text("replacement")
            self.command("replace-tree", staged, installed)
            self.assertEqual(
                sorted(p.name for p in installed.iterdir()), ["replacement.conf"]
            )
            self.assertEqual((staged / "new.conf").read_text(), "new")


if __name__ == "__main__":
    unittest.main()
