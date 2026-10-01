import os.path
import shlex

import ExecUtils
from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
SSH_SCRIPT = os.path.abspath(os.path.join(SCRIPT_DIR, "ssh.sh"))
SCP_SCRIPT = os.path.abspath(os.path.join(SCRIPT_DIR, "../vm/scp.sh"))


@library
class SSH:
    @keyword
    async def execute(self, command: str, timeout: int | None = 30) -> str:
        """
        Run a command via SSH and return its output.

        Args:
            command: The command to run.
            timeout: Duration in seconds after which the command is terminated
                     if it's still running. The default timeout is 30 seconds.
                     Use 'None' to run without timeout.
        Returns:
            The output of the command.
        """
        result = ExecUtils.run(
            [SSH_SCRIPT, "--", command],
            check=True,
            capture_output=True,
            text=True,
            timeout=timeout,
        )

        stdout = result.stdout.strip()
        if len(stdout) == 0:
            logger.debug("stdout: <empty>")
        else:
            logger.debug(f"stdout: {stdout}")

        stderr = result.stderr.strip()
        if len(stderr) == 0:
            logger.debug("stderr: <empty>")
        else:
            logger.debug(f"stderr: {stderr}")

        return stdout

    @keyword
    async def execute_with_status(
        self, command: str, timeout: int | None = 30
    ) -> tuple[str, str, int]:
        """
        Run a command via SSH and return stdout, stderr, and its exit status.
        """
        result = ExecUtils.run(
            [SSH_SCRIPT, "--", command],
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )

        stdout = result.stdout.strip()
        if len(stdout) == 0:
            logger.debug("stdout: <empty>")
        else:
            logger.debug(f"stdout: {stdout}")

        stderr = result.stderr.strip()
        if len(stderr) == 0:
            logger.debug("stderr: <empty>")
        else:
            logger.debug(f"stderr: {stderr}")

        return stdout, stderr, result.returncode

    @keyword
    async def copy_to_vm(self, local_path: str, remote_path: str) -> None:
        """
        Copy a local file to the VM over its VSOCK SSH connection.

        Args:
            local_path: Path to the local file.
            remote_path: Destination path in the VM.
        """
        ExecUtils.run(
            [SCP_SCRIPT, local_path, remote_path],
            check=True,
            capture_output=True,
            text=True,
        )

    @keyword
    async def execute_as_user(
        self,
        user: str,
        command: str,
        timeout: int | None = 30,
        extra_env: dict[str, str] | None = None,
    ) -> str:
        """
        Run a command via SSH as a specific user and return its output.
        """
        extra_env_args = " ".join(
            f"{name}={shlex.quote(value)}" for name, value in (extra_env or {}).items()
        )
        if extra_env_args:
            extra_env_args += " "
        command = (
            f"sudo -u {user} "
            f"XDG_RUNTIME_DIR=/run/user/$(id -u {user}) "
            f"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$(id -u {user})/bus "
            f"{extra_env_args}"
            "-- "
            f'sh -c "{command}"'
        )
        return await self.execute(command, timeout)

    @keyword
    async def execute_as_current_user(
        self, command: str, timeout: int | None = 30
    ) -> str:
        # Get the user that is currently logged in by checking which user the
        # gnome-shell process runs as
        stdout = await self.execute("ps -C gnome-shell -o user=,pid=")
        sessions = [line.split() for line in stdout.splitlines() if line.strip()]
        if len(sessions) == 0:
            raise RuntimeError("No user is currently logged in")
        elif len(sessions) > 1:
            users = [session[0] for session in sessions]
            raise RuntimeError(f"Multiple users are logged in: {users}")
        user, pid = sessions[0]
        logger.info(f"Running command as user '{user}'")

        # GTK apps launched over SSH need the active desktop session's display environment.
        session_env_output = await self.execute(
            f"tr '\\0' '\\n' < /proc/{pid}/environ | "
            "grep -E '^(DISPLAY|WAYLAND_DISPLAY|XAUTHORITY)=' || true"
        )
        session_env = {}
        for line in session_env_output.splitlines():
            name, separator, value = line.partition("=")
            if separator:
                session_env[name] = value

        return await self.execute_as_user(user, command, timeout, extra_env=session_env)
