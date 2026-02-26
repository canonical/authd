import os
from ansi2html import Ansi2HTMLConverter
import select
import subprocess
import time

from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore
from robot.libraries.BuiltIn import BuiltIn

import ExecUtils
import VMUtils

HOST_CID = 2 # 2 always refers to the host
PORT = 55000


@library
class Journal:
    process = None
    output_dir = None

    @keyword
    async def start_receiving_journal(self) -> None:
        """
        Start receiving journal entries from the VM via vsock.
        """
        if self.process:
            return

        output_dir = BuiltIn().get_variable_value('${OUTPUT DIR}', '.')
        suite_name = BuiltIn().get_variable_value('${SUITE NAME}', 'unknown')
        self.output_dir = os.path.join(output_dir, suite_name, "journal")
        os.makedirs(self.output_dir, exist_ok=True)

        if os.getenv("SYSTEMD_SUPPORTS_VSOCK"):
            self.process = ExecUtils.Popen(
                [
                    "/lib/systemd/systemd-journal-remote",
                    f"--listen-raw=vsock:{HOST_CID}:{PORT}",
                    f"--output={self.output_dir}",
                ],
            )
        else:
            self.process = stream_journal_from_vm_via_tcp(output_dir=self.output_dir)

    @keyword
    async def stop_receiving_journal(self) -> None:
        """
        Stop receiving journal entries from the VM.
        """
        if self.process:
            self.process.terminate()
            self.process.wait()
            self.process = None

    @keyword
    async def log_journal(self) -> None:
        """
        Log the journal entries received from the VM.
        """
        output = ExecUtils.check_output(
            [
                'journalctl',
                '--no-pager',
                '--directory', self.output_dir,
            ],
            env={'SYSTEMD_COLORS': 'true'},
            text=True,
        )

        html_output = Ansi2HTMLConverter(inline=True).convert(output, full=False)
        logger.info(html_output, html=True)

def stream_journal_from_vm_via_tcp(output_dir, timeout=60):
    vm_name = VMUtils.vm_name()
    vm_ip = VMUtils.vm_ip()
    deadline = time.time() + timeout

    while time.time() < deadline:
        # Start socat to connect to the VM's TCP port
        socat = subprocess.Popen(
            ["socat", "-d", "-d", f"TCP:{vm_ip}:{PORT}", "-"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

        connected = False
        stderr_buf = []

        # Read socat's stderr until we see a successful connection or timeout
        while True:
            r, _, _ = select.select([socat.stderr], [], [], 1)
            if not r:
                if socat.poll() is not None:
                    break
                continue

            line = socat.stderr.readline()
            if not line:
                break

            stderr_buf.append(line)

            if "successfully connected" in line:
                connected = True
                break

        if not connected:
            logger.error("".join(stderr_buf))
            socat.kill()
            time.sleep(1)
            continue

        # TCP connection confirmed
        journal_remote = subprocess.Popen(
            [
                "/lib/systemd/systemd-journal-remote",
                f"--output={output_dir}/{vm_name}.journal",
                "-",
            ],
            stdin=socat.stdout,
        )

        socat.stdout.close()
        return journal_remote

    raise RuntimeError(
        f"Failed to connect to VM journal stream within {timeout}s"
    )
