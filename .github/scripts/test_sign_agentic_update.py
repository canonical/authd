import base64
import hashlib
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from sign_agentic_update import BOT_EMAIL, api, sign_update


class SignUpdateTests(unittest.TestCase):
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
        Path("old").write_text("old\n")
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "base")
        self.parent = self.git("rev-parse", "HEAD").decode().strip()
        self.calls = []
        self.verified = True
        self.email = BOT_EMAIL

    def git(self, *args):
        return subprocess.check_output(["git", "--no-pager", *args])

    def api(self, repository, endpoint, body):
        self.assertEqual(repository, "canonical/authd")
        self.calls.append((endpoint, body))
        if endpoint == "blobs":
            data = base64.b64decode(body["content"])
            return {
                "sha": hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()
            }
        if endpoint == "trees":
            return {"sha": self.git("write-tree").decode().strip()}
        self.assertEqual(endpoint, "commits")
        self.assertEqual(set(body), {"parents", "tree", "message"})
        headers = (
            f"tree {body['tree']}\nparent {body['parents'][0]}\n"
            f"author github-actions[bot] <{self.email}> 1234567890 +0000\n"
            "committer GitHub <noreply@github.com> 1234567890 +0000"
        )
        signature = (
            "-----BEGIN PGP SIGNATURE-----\n\nfixture\n-----END PGP SIGNATURE-----\n"
        )
        raw = (
            f"{headers}\ngpgsig -----BEGIN PGP SIGNATURE-----\n"
            f" \n fixture\n -----END PGP SIGNATURE-----\n \n\n{body['message']}"
        ).encode()
        sha = hashlib.sha1(f"commit {len(raw)}\0".encode() + raw).hexdigest()
        return {
            "sha": sha,
            "author": {"email": self.email},
            "verification": {
                "verified": self.verified,
                "payload": f"{headers}\n\n{body['message']}",
                "signature": signature,
            },
        }

    def stage_update(self):
        Path(".github/workflows").mkdir(parents=True)
        Path(".github/workflows/update.yaml").write_text("on: workflow_dispatch\n")
        self.git("add", "-A")

    def test_installs_the_exact_signed_commit_without_publishing_a_ref(self):
        self.stage_update()
        tree = self.git("write-tree").decode().strip()
        with patch("sign_agentic_update.api", self.api):
            sha = sign_update("canonical/authd", "Automated update\n\nRun: fixture")
        self.assertEqual(self.git("rev-parse", "HEAD").decode().strip(), sha)
        self.assertEqual(self.git("rev-parse", "HEAD^").decode().strip(), self.parent)
        self.assertEqual(self.git("rev-parse", "HEAD^{tree}").decode().strip(), tree)
        self.assertEqual(self.git("status", "--porcelain"), b"")
        self.assertIn(
            b"gpgsig -----BEGIN PGP SIGNATURE-----", self.git("cat-file", "commit", sha)
        )
        self.assertIn(
            b" -----END PGP SIGNATURE-----\n \n\n",
            self.git("cat-file", "commit", sha),
        )
        self.assertEqual(
            [endpoint for endpoint, _ in self.calls], ["blobs", "trees", "commits"]
        )

    def test_preserves_binary_files_renames_deletions_symlinks_and_modes(self):
        Path("old").rename("renamed")
        Path("binary").write_bytes(b"\x00\xff\n")
        Path("executable").write_text("#!/bin/sh\n")
        Path("executable").chmod(0o755)
        Path("link").symlink_to("renamed")
        self.git("add", "-A")
        self.git(
            "update-index", "--add", "--cacheinfo", "160000", self.parent, "submodule"
        )
        with patch("sign_agentic_update.api", self.api):
            sign_update("canonical/authd", "Update")
        entries = next(
            body["tree"] for endpoint, body in self.calls if endpoint == "trees"
        )
        entries = {entry["path"]: entry for entry in entries}
        self.assertIsNone(entries["old"]["sha"])
        self.assertEqual(entries["executable"]["mode"], "100755")
        self.assertEqual(entries["link"]["mode"], "120000")
        self.assertEqual(entries["submodule"]["type"], "commit")
        self.assertEqual(entries["submodule"]["sha"], self.parent)
        self.assertEqual(self.git("show", "HEAD:binary"), b"\x00\xff\n")
        self.assertEqual(self.git("show", "HEAD:renamed"), b"old\n")

    def test_rejects_unsigned_or_non_bot_commits_without_moving_head(self):
        self.stage_update()
        for verified, email in [(False, BOT_EMAIL), (True, "human@example.com")]:
            with self.subTest(verified=verified, email=email):
                self.verified, self.email = verified, email
                with (
                    patch("sign_agentic_update.api", self.api),
                    self.assertRaises(RuntimeError),
                ):
                    sign_update("canonical/authd", "Update")
                self.assertEqual(
                    self.git("rev-parse", "HEAD").decode().strip(), self.parent
                )

    def test_rejects_a_changed_tree_or_signature_without_moving_head(self):
        self.stage_update()
        for broken in ["tree", "signature"]:
            with self.subTest(broken=broken):

                def api(repository, endpoint, body):
                    response = self.api(repository, endpoint, body)
                    if broken == "tree" and endpoint == "trees":
                        response["sha"] = "0" * 40
                    if broken == "signature" and endpoint == "commits":
                        response["verification"]["signature"] += "tampered"
                    return response

                with (
                    patch("sign_agentic_update.api", api),
                    self.assertRaises(RuntimeError),
                ):
                    sign_update("canonical/authd", "Update")
                self.assertEqual(
                    self.git("rev-parse", "HEAD").decode().strip(), self.parent
                )

    def test_skips_an_empty_update(self):
        with patch("sign_agentic_update.api") as api:
            self.assertEqual(sign_update("canonical/authd", "Update"), self.parent)
        api.assert_not_called()

    def test_api_failures_report_the_boundary_without_disclosing_response_data(self):
        result = subprocess.CompletedProcess(
            ["gh"],
            1,
            stdout=b"sensitive response",
            stderr=b"gh: sensitive response (HTTP 403)",
        )
        with (
            patch.dict(os.environ, {"WORKFLOW_UPDATE_TOKEN": "tree-test-token"}),
            patch("sign_agentic_update.subprocess.run", return_value=result),
        ):
            with self.assertRaisesRegex(
                RuntimeError, r"^GitHub Git trees request failed \(HTTP 403\)$"
            ):
                api("canonical/authd", "trees", {})

    def test_api_uses_the_pat_for_trees_and_the_bot_token_for_commits(self):
        result = subprocess.CompletedProcess(["gh"], 0, stdout=b"{}", stderr=b"")
        with (
            patch.dict(
                os.environ,
                {
                    "GH_TOKEN": "bot-test-token",
                    "WORKFLOW_UPDATE_TOKEN": "tree-test-token",
                },
                clear=True,
            ),
            patch("sign_agentic_update.subprocess.run", return_value=result) as run,
        ):
            for endpoint in ["blobs", "trees", "commits"]:
                with self.subTest(endpoint=endpoint):
                    api("canonical/authd", endpoint, {})
                    env = run.call_args.kwargs["env"]
                    self.assertEqual(
                        env["GH_TOKEN"],
                        "tree-test-token" if endpoint == "trees" else "bot-test-token",
                    )
                    self.assertNotIn("WORKFLOW_UPDATE_TOKEN", env)


if __name__ == "__main__":
    unittest.main()
