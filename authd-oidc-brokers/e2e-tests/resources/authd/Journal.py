import os
from ansi2html import Ansi2HTMLConverter

from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore
from robot.libraries.BuiltIn import BuiltIn

import ExecUtils

HOST_CID = 2 # 2 always refers to the host
PORT = 55000

def libvirt_host_ip() -> str:
    # Get the bridge name for the default libvirt network
    bridge = ExecUtils.check_output(
        ["virsh", "net-info", "default"],
        text=True,
    ).split("Bridge:")[1].split()[0]

    # Get the IPv4 address assigned to that bridge
    ip = ExecUtils.check_output(
        ["ip", "-4", "addr", "show", bridge],
        text=True,
    )

    host_ip = next(
        line.split()[1].split("/")[0]
        for line in ip.splitlines()
        if line.strip().startswith("inet ")
    )
    return host_ip

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
            listen_address = f"vsock:{HOST_CID}:{PORT}"
        else:
            host_ip = libvirt_host_ip()
            listen_address = f"{host_ip}:{PORT}"

        self.process = ExecUtils.Popen(
            [
                "/lib/systemd/systemd-journal-remote",
                f"--listen-raw={listen_address}",
                f"--output={self.output_dir}"
            ],
        )

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
