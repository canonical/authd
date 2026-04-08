import os
import subprocess

from robot.api.deco import keyword, library  # type: ignore
from robot.api import logger

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

# Map broker names to their broker-specific browser login scripts.
_BROKER_LOGIN_SCRIPTS = {
    "authd-msentraid": os.path.join(SCRIPT_DIR, "browser_login", "msentraid.py"),
    "authd-google": os.path.join(SCRIPT_DIR, "browser_login", "google.py"),
}


@library
class Browser:
    """Library for browser automation using a headless browser."""
    @keyword
    async def login(self, usercode: str, output_dir: str = "."):
        """Perform device authentication with the given username, password and
        usercode using a broker-specific browser automation script. The window
        opened by the script is run off screen using Xvfb.
        The BROKER environment variable selects which login script to use.
        """
        broker = os.environ.get("BROKER")
        script = _BROKER_LOGIN_SCRIPTS.get(broker)
        if not script:
            raise ValueError(
                f"Unknown broker: {broker!r}. "
                f"Known brokers: {sorted(_BROKER_LOGIN_SCRIPTS.keys())}"
            )
        command = [
            script,
            usercode,
            "--output-dir", output_dir,
        ]
        if not os.getenv("SHOW_WEBVIEW"):
            command = [
                          "/usr/bin/env",
                          "GDK_BACKEND=x11",
                          "xvfb-run", "-a",
                          "--",
                      ] + command
        result = subprocess.run(command, stderr=subprocess.PIPE, text=True)
        if result.returncode == 0:
            return
        logger.error(f"Command '{' '.join(command)}' failed with exit code {result.returncode}")
        raise RuntimeError(f"Browser login failed\n{result.stderr}")
