import os
import subprocess
import sys
import threading

from robot.api.deco import keyword, library  # type: ignore
from robot.api import logger

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
BROWSER_LOGIN_DIR = os.path.join(SCRIPT_DIR, "browser_login")

# Map broker names to their broker-specific browser login scripts.
_BROKER_LOGIN_SCRIPTS = {
    "authd-msentraid": os.path.join(BROWSER_LOGIN_DIR, "msentraid.py"),
    "authd-google": os.path.join(BROWSER_LOGIN_DIR, "google.py"),
}

# Write directly to the real stderr, bypassing Robot Framework's capture.
# This matches what Listener.py does so the output ends up in the same stream.
_out = sys.__stderr__


def _stream_to_stderr(stream, lines):
    """Read *stream* line by line, write each line to stderr immediately, and
    collect all lines into *lines* for later logging via Robot Framework."""
    for line in stream:
        stripped = line.rstrip("\n")
        if stripped:
            _out.write(stripped + "\n")
            _out.flush()
            lines.append(stripped)


@library
class Browser:
    """Library for browser automation using a headless browser."""

    @keyword
    def login(self, usercode: str, output_dir: str = "."):
        """Perform device authentication with the given username, password and
        usercode using a broker-specific browser automation script. The window
        opened by the script is run off screen using Xvfb unless ``SHOW_WEBVIEW``
        is set. Output is streamed to stderr in real time and also logged to the
        Robot Framework log file after the process completes.
        The ``BROKER`` environment variable selects which login script to use.
        """
        broker = os.environ.get("BROKER")
        script = _BROKER_LOGIN_SCRIPTS.get(broker)
        if not script:
            raise ValueError(
                f"Unknown broker: {broker!r}. "
                f"Known brokers: {sorted(_BROKER_LOGIN_SCRIPTS.keys())}"
            )

        command = [script, usercode, "--output-dir", output_dir]
        if not os.getenv("SHOW_WEBVIEW"):
            command = ["/usr/bin/env", "GDK_BACKEND=x11", "xvfb-run", "-a", "--"] + command

        lines = []
        process = subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        stdout_thread = threading.Thread(
            target=_stream_to_stderr, args=(process.stdout, lines), daemon=True
        )
        stdout_thread.start()
        process.wait()
        stdout_thread.join()

        for line in lines:
            logger.info(line)

        if process.returncode != 0:
            raise RuntimeError(
                f"Browser login failed (exit code {process.returncode})"
            )
