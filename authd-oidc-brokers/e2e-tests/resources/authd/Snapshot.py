import os

from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore

import ExecUtils

VM_NAME_BASE="e2e-runner"

def vm_name() -> str:
    release = os.environ.get("RELEASE")
    if not release:
        raise Exception("RELEASE environment variable is not set")
    return f"{VM_NAME_BASE}-{release}"

@library
class Snapshot:
    @keyword
    async def restore(self, name: str) -> None:
        """
        Revert the VM to the specified snapshot.

        Args:
            name: The name of the snapshot to revert to.
        """
        ExecUtils.run(
            ["virsh", "snapshot-revert", vm_name(), name],
            check=True,
        )
