import subprocess

from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore

import ExecUtils
import VMUtils


@library
class Snapshot:
    @keyword
    async def restore(self, name: str) -> None:
        """
        Revert the VM to the specified snapshot.

        Args:
            name: The name of the snapshot to revert to.
        """
        vm_name = VMUtils.vm_name()
        process = ExecUtils.run(
            ["virsh", "snapshot-revert", vm_name, name],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True
        )
        logger.info("snapshot-revert output:\n" + process.stdout)
