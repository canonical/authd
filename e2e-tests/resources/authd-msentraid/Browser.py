import os
import subprocess

from robot.api.deco import keyword, library  # type: ignore
from robot.api import logger

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))


@library
class Browser:
    """Library for browser automation using a headless browser.
    """

    @keyword
    async def login(self, usercode: str, output_dir: str = "."):
        """Perform device authentication with the given username, password and
        usercode using a browser automation script. The window opened by the
        script is run off screen using Xvfb.
        """
        command = [
            os.path.join(SCRIPT_DIR, "browser_login.py"),
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
