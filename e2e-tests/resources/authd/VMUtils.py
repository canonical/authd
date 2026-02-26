import os
import subprocess
import time

VM_NAME_BASE="e2e-runner"

def vm_name() -> str:
    release = os.environ.get("RELEASE")
    if not release:
        raise Exception("RELEASE environment variable is not set")
    return f"{VM_NAME_BASE}-{release}"


def vm_ip(timeout=60):
    deadline = time.time() + timeout

    while time.time() < deadline:
        p = subprocess.run(
            ["virsh", "domifaddr", "--domain", vm_name(), "--source", "agent"],
            stdout=subprocess.PIPE,
            text=True,
            check=False,
        )

        for line in p.stdout.splitlines():
            parts = line.split()
            if len(parts) >= 4 and parts[2] == "ipv4":
                ip = parts[3].split("/")[0]
                if ip != "127.0.0.1":
                    return ip

        time.sleep(1)

    raise RuntimeError(f"Timed out waiting for IPv4 address of VM '{vm_name()}'")
