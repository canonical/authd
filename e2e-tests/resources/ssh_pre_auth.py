"""Exercise stale SSH identities inside the E2E VM.

This helper changes the test account's stored alias. Do not run it on a
production system.
"""

import contextlib
import json
import os
import pwd
import re
import select
import shlex
import socket
import sqlite3
import subprocess
import sys
import tempfile
from pathlib import Path

IDENTITY_COMMAND = """
import json, os, pwd, sys, tempfile
def identity():
    fd, path = tempfile.mkstemp()
    file_uid = os.fstat(fd).st_uid
    os.close(fd)
    os.unlink(path)
    print(json.dumps(dict(name=pwd.getpwuid(os.getuid()).pw_name,
        uid=os.getuid(), gid=os.getgid(), home=os.environ["HOME"],
        groups=sorted(os.getgroups()), file_uid=file_uid)), flush=True)
identity()
for line in sys.stdin:
    identity()
"""


def askpass(path):
    """Wait for the test to release a password prompt."""
    if os.environ.get("SSH_ASKPASS_PROMPT") == "none":
        return
    with socket.socket(socket.AF_UNIX) as connection:
        connection.connect(path)
        connection.sendall(json.dumps(sys.argv[-1]).encode() + b"\n")
        with connection.makefile("r") as response:
            print(response.readline().rstrip("\n"))


class SSHConnection:
    """An SSH client whose password prompt can be held open."""

    def __init__(self, directory, name, username, password, attempts=1):
        self.password = password
        self.pending = None
        path = str(Path(directory, f"{name}.socket"))
        self.listener = socket.socket(socket.AF_UNIX)
        self.listener.bind(path)
        self.listener.listen()
        self.listener.settimeout(45)
        askpass_path = Path(directory, f"{name}.askpass")
        askpass_path.write_text(
            "#!/bin/sh\nexec python3 "
            f'{shlex.quote(__file__)} askpass {shlex.quote(path)} "$@"\n'
        )
        askpass_path.chmod(0o700)
        self.process = subprocess.Popen(
            [
                "ssh",
                "-o",
                "StrictHostKeyChecking=accept-new",
                "-o",
                "PreferredAuthentications=keyboard-interactive",
                "-o",
                f"NumberOfPasswordPrompts={attempts}",
                f"{username}@localhost",
                f"python3 -u -c {shlex.quote(IDENTITY_COMMAND)}",
            ],
            env={
                **os.environ,
                "SSH_ASKPASS": str(askpass_path),
                "SSH_ASKPASS_REQUIRE": "force",
                "DISPLAY": ":0",
            },
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            start_new_session=True,
        )

    def wait_for_password(self):
        """Reach a password challenge without completing authentication."""
        while True:
            connection, _ = self.listener.accept()
            connection.settimeout(45)
            with connection.makefile("r") as prompt_file:
                prompt = json.loads(prompt_file.readline()).lower()
            if "choose your provider" in prompt:
                response = "2"
            elif "choose action" in prompt:
                response = "1"
            elif "password" in prompt:
                self.pending = connection
                return
            else:
                connection.close()
                raise AssertionError(f"Unexpected SSH prompt: {prompt}")
            connection.sendall(response.encode() + b"\n")
            connection.close()

    def release(self):
        """Let the pending authentication complete."""
        self.pending.sendall(self.password.encode() + b"\n")
        self.pending.close()
        self.pending = None

    def read_identity(self):
        """Read a live session's identity without a timing-based sleep."""
        ready, _, _ = select.select([self.process.stdout], [], [], 45)
        assert ready, "SSH did not start a session"
        line = self.process.stdout.readline()
        assert line, (
            f"SSH exited before starting a session: {self.process.stderr.read()}"
        )
        return json.loads(line)

    def finish(self, allowed, unix_name):
        """Check that SSH rejects the connection or uses the stored identity."""
        stdout, stderr = self.process.communicate(timeout=45)
        if not allowed:
            assert self.process.returncode == 255, (stdout, stderr)
            assert not stdout, f"A rejected connection started a session: {stdout}"
            return
        assert self.process.returncode == 0, stderr
        check_identity(json.loads(stdout), unix_name)

    def close(self):
        """Release sockets and stop only this client's process."""
        if self.pending is not None:
            self.pending.close()
        self.listener.close()
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.communicate(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.communicate()


def check_identity(identity, unix_name):
    """Compare the kernel identity and file owner with the real NSS account."""
    stored = pwd.getpwnam(unix_name)
    assert identity == {
        "name": stored.pw_name,
        "uid": stored.pw_uid,
        "gid": stored.pw_gid,
        "home": stored.pw_dir,
        "groups": sorted(os.getgrouplist(unix_name, stored.pw_gid)),
        "file_uid": stored.pw_uid,
    }, identity


def temporary_uid(username):
    """Read the pre-auth UID from this authd invocation's journal.

    NSS only exposes temporary users to sshd and its direct children.
    """
    invocation = subprocess.run(
        ["systemctl", "show", "--property=InvocationID", "--value", "authd.service"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    assert invocation, "authd.service has no current invocation"
    subprocess.run(["journalctl", "--sync"], check=True)
    journal = subprocess.run(
        [
            "journalctl",
            f"_SYSTEMD_INVOCATION_ID={invocation}",
            "-o",
            "cat",
            "--no-pager",
        ],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    pattern = rf'Using new UID (\d+) for temporary user "{re.escape(username.lower())}"'
    uids = re.findall(pattern, journal)
    assert uids, f"authd did not log a temporary UID for {username}"
    return int(uids[-1])


def forget_alias(unix_name):
    """Keep an old alias so the next login must replace it."""
    subprocess.run(["systemctl", "stop", "authd.socket", "authd.service"], check=True)
    with sqlite3.connect("/var/lib/authd/authd.sqlite3") as database:
        changed = database.execute(
            "UPDATE users SET provider_username = ? WHERE name = ? AND provider_id != ''",
            ("old-alias@example.com", unix_name),
        )
        assert changed.rowcount == 1, "Expected one provider-ID-bound account"
    subprocess.run(["systemctl", "start", "authd.socket", "authd.service"], check=True)


def run(username, unix_name, password):
    """Cover overlapping stale connections and a restart with a live session."""
    release = Path("/etc/os-release").read_text()
    aliases_allowed = 'VERSION_ID="24.04"' not in release
    with tempfile.TemporaryDirectory(prefix="authd-ssh-") as directory:
        with contextlib.ExitStack() as cleanup:

            def connect(name, login_name, attempts=1):
                client = SSHConnection(directory, name, login_name, password, attempts)
                cleanup.callback(client.close)
                client.wait_for_password()
                return client

            forget_alias(unix_name)
            first = connect("first", username)
            temporary = temporary_uid(username)
            second = connect("second", username, attempts=2)
            assert temporary_uid(username) == temporary
            assert pwd.getpwnam(unix_name).pw_uid != temporary

            first.release()
            first.finish(False, unix_name)
            assert pwd.getpwnam(username).pw_uid == pwd.getpwnam(unix_name).pw_uid
            second.release()
            second.wait_for_password()
            second.release()
            second.finish(False, unix_name)
            fresh = connect("fresh", username)
            fresh.release()
            fresh.finish(aliases_allowed, unix_name)

            live = connect("live", unix_name)
            live.release()
            check_identity(live.read_identity(), unix_name)
            # Updating the stored alias also restarts authd.
            forget_alias(unix_name)
            live.process.stdin.write("check\n")
            live.process.stdin.flush()
            live.finish(True, unix_name)

            recovery = connect("recovery", username)
            recovery.release()
            recovery.finish(False, unix_name)
            fresh = connect("after-restart", unix_name)
            fresh.release()
            fresh.finish(True, unix_name)
    print("Overlapping SSH connections and the live-session restart passed.")


if __name__ == "__main__":
    if sys.argv[1] == "askpass":
        askpass(sys.argv[2])
    else:
        run(sys.argv[1], sys.argv[2], sys.argv[3])
