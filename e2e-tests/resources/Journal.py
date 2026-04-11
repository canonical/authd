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
    socat_process = None
    output_dir = None

    @keyword
    async def start_receiving_journal(self) -> None:
        """
        Start receiving journal entries from the VM via vsock.
        """
        if self.process:
            return

        suite_output_dir = BuiltIn().get_variable_value('${SUITE_OUTPUT_DIR}')
        self.output_dir = os.path.join(suite_output_dir, "journal")
        os.makedirs(self.output_dir, exist_ok=True)

        if os.getenv("SYSTEMD_SUPPORTS_VSOCK"):
            self.process = ExecUtils.Popen(
                [
                    "/lib/systemd/systemd-journal-remote",
                    f"--listen-raw=vsock:{HOST_CID}:{PORT}",
                    f"--output={self.output_dir}",
                ],
                stderr=subprocess.PIPE,
            )
        else:
            self.process, self.socat_process = stream_journal_from_vm_via_tcp(output_dir=self.output_dir)

    @keyword
    async def stop_receiving_journal(self) -> None:
        """
        Stop receiving journal entries from the VM.
        """
        if self.socat_process:
            logger.info("Terminating socat")
            self.socat_process.terminate()
            self.socat_process.wait()
            socat_stderr = self.socat_process.stderr.read().decode()
            socat_stderr_filtered = _filter_socat_stderr(socat_stderr)
            logger.info("socat stderr:\n" + socat_stderr_filtered)
            self.socat_process = None
            # The systemd-journal-remote process should exit on its own when socat terminates
            try:
                self.process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                logger.error("systemd-journal-remote did not exit after socat termination, killing it")
                self.process.kill()
                self.process.wait()

            logger.info("systemd-journal-remote stderr:\n" + self.process.stderr.read().decode())
            self.process = None

        elif self.process:
            self.process.terminate()
            self.process.wait()
            logger.info("systemd-journal-remote stderr:\n" + self.process.stderr.read().decode())
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

        # ansi2html produces a <pre> block; split on newlines so we can wrap each
        # line in a <span> that JS can toggle.
        lines = html_output.split('\n')
        wrapped_lines = ''.join(
            f'<span class="jline" style="display:block">{line}</span>'
            for line in lines
        )

        uid = id(html_output)  # unique enough within a single report
        container_id = f'journal-{uid}'
        filter_id = f'journal-filter-{uid}'
        count_id = f'journal-count-{uid}'

        html = f"""
            <input id="{filter_id}"
              type="text"
              placeholder="Filter journal (plain text or /regex/i)"
              style="width:60%;padding:4px 6px;font-size:0.9em;box-sizing:border-box;margin:0"
              oninput="(function(){{
                var raw = document.getElementById('{filter_id}').value;
                var pre = document.getElementById('{container_id}');
                var lines = pre.querySelectorAll('.jline');
                var re = null;
                var m = raw.match(/^\\/(.+)\\/([gimsuy]*)$/);
                if (m) {{
                  try {{ re = new RegExp(m[1], m[2]); }} catch(e) {{ re = null; }}
                }}
                var shown = 0;
                lines.forEach(function(s) {{
                  var text = s.textContent;
                  var visible = raw === '' || (re ? re.test(text) : text.toLowerCase().indexOf(raw.toLowerCase()) !== -1);
                  s.style.display = visible ? 'block' : 'none';
                  if (visible) shown++;
                }});
                document.getElementById('{count_id}').textContent =
                  raw === '' ? '' : shown + ' / ' + lines.length + ' lines';
              }})()">
            <span id="{count_id}" style="margin-left:8px;font-size:0.85em;color:#888"></span>
            <pre id="{container_id}" style="background:#1b1b1b;color:#f8f8f2;overflow:auto;max-height:600px;margin:2px 0 0 0">{wrapped_lines}</pre>
            """
        logger.info(html, html=True)

def _filter_socat_stderr(stderr):
    """Filter socat stderr, keeping the first write/read line and summarizing the rest."""
    lines = []
    skipped = 0
    first_io_seen = False
    for line in stderr.splitlines():
        if "write(" in line:
            if not first_io_seen:
                lines.append(line)
                first_io_seen = True
            else:
                skipped += 1
        else:
            lines.append(line)
    if skipped:
        lines.append(f"... ({skipped} more write lines omitted)")
    return "\n".join(lines)


def stream_journal_from_vm_via_tcp(output_dir, timeout=60):
    vm_name = VMUtils.vm_name()
    vm_ip = VMUtils.vm_ip()
    deadline = time.time() + timeout

    while time.time() < deadline:
        # Start socat to connect to the VM's TCP port
        socat = subprocess.Popen(
            ["socat", "-u", "-d", "-d", f"TCP:{vm_ip}:{PORT}", "STDOUT"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False, # `journalctl -o export` produces binary output
            bufsize=0,
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

            line = socat.stderr.readline().decode(errors="replace")
            if not line:
                break

            stderr_buf.append(line)

            if "successfully connected" in line:
                logger.info("socat successfully connected to VM journal stream:\n" + "".join(stderr_buf))
                stderr_buf.clear()
                connected = True
                break

        if not connected:
            logger.info("socat failed to connect, retrying...\n" + "".join(stderr_buf))
            stderr_buf.clear()
            socat.kill()
            time.sleep(1)
            continue

        # TCP connection confirmed, start systemd-journal-remote to read from
        # socat's stdout
        journal_remote = subprocess.Popen(
            [
                "/lib/systemd/systemd-journal-remote",
                f"--output={output_dir}/{vm_name}.journal",
                "-",
            ],
            stderr=subprocess.PIPE,
            stdin=socat.stdout,
        )

        socat.stdout.close()
        return journal_remote, socat

    raise RuntimeError(
        f"Failed to connect to VM journal stream within {timeout}s"
    )
