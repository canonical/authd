import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

VM_DIR = Path(__file__).resolve().parent


class BackendTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.hooks = self.root / "backend hooks"
        self.hooks.mkdir()
        self.commands = self.root / "commands"
        self.commands.mkdir()
        self.log = self.root / "calls.json"
        self.config = self.root / "config.env"
        self.config.write_text("VM_NAME_BASE=custom\n")
        self.env = {
            **os.environ,
            "PATH": f"{self.commands}:/usr/bin:/bin",
            "CALL_LOG": str(self.log),
            "AUTHD_E2E_HOOKS_DIR": str(self.hooks),
            "AUTHD_E2E_DATA_DIR": str(self.root / "data"),
            "VM_NAME": "custom-resolute",
            "RELEASE": "resolute",
        }
        for variable in ("ARTIFACTS_DIR", "CI", "NO_SNAPSHOT", "FORCE"):
            self.env.pop(variable, None)
        self.script(
            self.hooks / "prepare-vm",
            """#!/usr/bin/python3
import json, os, pathlib, sys
pathlib.Path(os.environ["CALL_LOG"]).write_text(json.dumps({
    "args": sys.argv[1:],
    "data": os.environ["AUTHD_E2E_DATA_DIR"],
    "no_snapshot": os.environ["AUTHD_E2E_NO_SNAPSHOT"],
}))
directory = pathlib.Path(os.environ["ARTIFACTS_DIR"])
directory.mkdir(parents=True, exist_ok=True)
(directory / (sys.argv[1] + ".qcow2")).touch()
(directory / (os.environ["VM_NAME_BASE"] + ".xml")).touch()
""",
        )
        self.script(
            self.hooks / "virsh",
            """#!/usr/bin/python3
import os, sys
if sys.argv[1] == "snapshot-list":
    print(os.environ.get("SNAPSHOTS", "initial-setup"))
elif sys.argv[1] != "dominfo":
    print("|".join(sys.argv[1:]))
    print(sys.stdin.read(), end="")
    sys.exit(int(os.environ.get("HOOK_STATUS", "0")))
""",
        )

    def script(self, path, content):
        path.write_text(content)
        path.chmod(0o755)

    def provision(self, *args):
        return subprocess.run(
            [
                VM_DIR / "provision-ubuntu.sh",
                "--config-file",
                self.config,
                "--release",
                "resolute",
                *args,
            ],
            env=self.env,
            capture_output=True,
            text=True,
            timeout=10,
        )

    def test_prepare_receives_resolved_options_before_native_setup(self):
        data = self.root / "selected data"
        result = self.provision("--data-dir", str(data), "--force", "--no-snapshot")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            json.loads(self.log.read_text()),
            {
                "args": ["custom-resolute", "resolute", "true"],
                "data": str(data),
                "no_snapshot": "true",
            },
        )

    def test_prepare_knows_the_vm_name_base(self):
        self.env.pop("VM_NAME_BASE", None)
        self.config.write_text("")
        data = self.root / "data"
        result = self.provision("--data-dir", str(data))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((data / "resolute" / "e2e-runner.xml").exists())
        self.assertTrue((data / "resolute" / "custom-resolute.qcow2").exists())

    def test_prepare_failure_does_not_fall_back(self):
        self.script(self.hooks / "prepare-vm", "#!/bin/sh\nexit 42\n")
        result = self.provision()
        self.assertEqual(result.returncode, 42, result.stderr)
        self.assertFalse((self.root / "data").exists())

    def test_default_provisioning_still_checks_native_requirements(self):
        self.env.pop("AUTHD_E2E_HOOKS_DIR")
        self.env["SSH_PUBLIC_KEY_FILE"] = str(self.root / "missing.pub")
        result = self.provision()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SSH public key file not found", result.stdout)
        self.assertFalse(self.log.exists())

    def test_prepare_success_requires_artifacts_and_snapshot(self):
        self.script(self.hooks / "prepare-vm", "#!/bin/sh\nexit 0\n")
        result = self.provision()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("did not create the VM disk and definition", result.stderr)

    def test_prepare_success_requires_initial_snapshot(self):
        self.env["SNAPSHOTS"] = "initial-setup-other"
        result = self.provision()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("did not provide the initial-setup snapshot", result.stderr)
        self.assertEqual(json.loads(self.log.read_text())["no_snapshot"], "false")

    def test_snapshot_checks_consume_full_hook_output(self):
        self.script(
            self.hooks / "virsh",
            """#!/usr/bin/python3
import os, signal, sys
if sys.argv[1] == "snapshot-list":
    signal.signal(signal.SIGPIPE, signal.SIG_DFL)
    data = b"initial-setup\\n" + b"other-snapshot\\n" * 100000
    while data:
        data = data[os.write(1, data):]
""",
        )
        result = self.provision()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_configured_backend_must_have_both_executable_hooks(self):
        for hook in ("prepare-vm", "virsh"):
            with self.subTest(hook=hook):
                path = self.hooks / hook
                path.chmod(0o644)
                result = self.provision()
                path.chmod(0o755)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(
                    f"Missing executable E2E backend hook: {path}", result.stderr
                )
                self.assertFalse(self.log.exists())

    def test_relative_hook_directory_is_rejected(self):
        self.env["AUTHD_E2E_HOOKS_DIR"] = "relative-hooks"
        result = self.provision()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be an absolute directory", result.stderr)

    def test_direct_virsh_helper_validates_the_whole_backend(self):
        (self.hooks / "prepare-vm").unlink()
        result = subprocess.run(
            [VM_DIR / "helpers/virsh", "dominfo", "a vm"],
            env=self.env,
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Missing executable E2E backend hook:", result.stderr)

    def test_hook_virsh_cannot_recurse(self):
        self.script(self.hooks / "virsh", '#!/bin/sh\nexec virsh "$@"\n')
        result = subprocess.run(
            [VM_DIR / "helpers/virsh", "dominfo", "a vm"],
            env=self.env,
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must call /usr/bin/virsh", result.stderr)

    def test_child_processes_select_virsh_hook_and_preserve_exit_status(self):
        self.env["HOOK_STATUS"] = "37"
        result = subprocess.run(
            [
                "bash",
                "-c",
                'source "$1"; /usr/bin/python3 -c "$2"',
                "bash",
                str(VM_DIR / "lib/libprovision.sh"),
                "import subprocess; raise SystemExit(subprocess.call("
                '["virsh", "test-command", "argument with spaces"]))',
            ],
            input="stdin payload\n",
            env=self.env,
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 37, result.stderr)
        self.assertEqual(
            result.stdout, "test-command|argument with spaces\nstdin payload\n"
        )

    def test_python_vm_lookup_uses_resolved_backend_context(self):
        self.script(
            self.hooks / "virsh",
            """#!/usr/bin/python3
import os, sys
expected = os.environ.get("VM_NAME") or "e2e-runner-" + os.environ["RELEASE"]
assert sys.argv[1:] == ["domifaddr", "--domain", expected, "--source", "agent"]
print("vnet0 52:54:00:00:00:00 ipv4 192.0.2.7/24")
""",
        )
        for vm_name, release in (
            ("custom-resolute", "resolute"),
            ("custom-vm", None),
            (None, "resolute"),
        ):
            with self.subTest(vm_name=vm_name, release=release):
                env = self.env.copy()
                for key, value in (("VM_NAME", vm_name), ("RELEASE", release)):
                    if value is None:
                        env.pop(key, None)
                    else:
                        env[key] = value
                result = subprocess.run(
                    [
                        "bash",
                        "-c",
                        'source "$1"; /usr/bin/python3 -c "$2" "$3"',
                        "bash",
                        VM_DIR / "lib/libprovision.sh",
                        "import sys; sys.path.insert(0, sys.argv[1]); "
                        "import VMUtils; print(VMUtils.vm_ip(timeout=5))",
                        VM_DIR.parent / "resources",
                    ],
                    env=env,
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, "192.0.2.7\n")

    def test_default_virsh_keeps_sudo_and_connection_behavior(self):
        self.env.pop("AUTHD_E2E_HOOKS_DIR")
        self.script(self.commands / "sudo", '#!/bin/sh\nprintf "%s\\n" "$@"\n')
        for uri, expected in (
            (None, "qemu:///system"),
            ("qemu:///session", "qemu:///session"),
        ):
            with self.subTest(uri=uri):
                env = self.env.copy()
                env.pop("LIBVIRT_DEFAULT_URI", None)
                if uri:
                    env["LIBVIRT_DEFAULT_URI"] = uri
                result = subprocess.run(
                    [VM_DIR / "helpers/virsh", "dominfo", "a vm"],
                    env=env,
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    result.stdout.splitlines(),
                    [
                        f"LIBVIRT_DEFAULT_URI={expected}",
                        "/usr/bin/virsh",
                        "dominfo",
                        "a vm",
                    ],
                )

    def test_standalone_ssh_and_scp_export_vm_context_to_hook(self):
        self.env.pop("VM_NAME")
        self.script(
            self.hooks / "virsh",
            """#!/usr/bin/python3
import os
assert os.environ["VM_NAME"] == "e2e-runner-resolute"
assert os.environ["RELEASE"] == "resolute"
print('<domain><vsock><cid address="7"/></vsock></domain>')
""",
        )
        self.script(
            self.commands / "xmllint",
            '#!/usr/bin/python3\nimport sys\nassert "domain" in sys.stdin.read()\nprint(7)\n',
        )
        for command, args, unset_release in (
            ("ssh", ["--release", "resolute", "--", "hostname"], True),
            ("scp", ["local", "remote"], False),
        ):
            with self.subTest(command=command):
                env = self.env.copy()
                if unset_release:
                    env.pop("RELEASE", None)
                self.script(self.commands / command, '#!/bin/sh\nprintf "%s\\n" "$@"\n')
                result = subprocess.run(
                    [VM_DIR / f"{command}.sh", *args],
                    env=env,
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("VSOCK-CONNECT:7:22", result.stdout)


if __name__ == "__main__":
    unittest.main()
