import os.path

from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore

import ExecUtils

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
SSH_SCRIPT = os.path.abspath(os.path.join(SCRIPT_DIR, "ssh.sh"))

@library
class SSH:
    @keyword
    async def execute(self, command: str, timeout: int|None = 30) -> str:
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
            logger.debug(f"stdout: <empty>")
        else:
            logger.debug(f"stdout: {stdout}")

        stderr = result.stderr.strip()
        if len(stderr) == 0:
            logger.debug(f"stderr: <empty>")
        else:
            logger.debug(f"stderr: {stderr}")

        return stdout
