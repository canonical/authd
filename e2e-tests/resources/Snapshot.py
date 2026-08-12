import os
import subprocess
import xml.etree.ElementTree as ET

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

    @keyword
    async def create(self, name: str) -> None:
        """Create an external memory snapshot of the running VM.

        The memory file is placed in the same directory as the VM's disk image,
        following the same convention as ``force_create_snapshot`` in
        ``vm/lib/libprovision.sh``::

            <image_dir>/<vm_name>-<snapshot_name>.mem

        Args:
            name: The name for the new snapshot.
        """
        vm_name = VMUtils.vm_name()
        mem_file = self._mem_file_path(vm_name, name)
        logger.info(f"Creating snapshot '{name}', memory file: {mem_file}")
        process = ExecUtils.run(
            ["virsh", "snapshot-create-as", vm_name, name,
             "--memspec", f"{mem_file},snapshot=external"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        logger.info("snapshot-create-as output:\n" + process.stdout)

    @keyword
    async def delete(self, name: str) -> None:
        """Delete a named snapshot.

        Args:
            name: The name of the snapshot to delete.
        """
        vm_name = VMUtils.vm_name()
        process = ExecUtils.run(
            ["virsh", "snapshot-delete", "--domain", vm_name,
             "--snapshotname", name],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        logger.info("snapshot-delete output:\n" + process.stdout)

    @keyword
    async def exists(self, name: str) -> bool:
        """Return ``True`` if a snapshot with the given name exists.

        Args:
            name: The snapshot name to look up.
        """
        vm_name = VMUtils.vm_name()
        process = ExecUtils.run(
            ["virsh", "snapshot-list", vm_name],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        return name in process.stdout

    def _mem_file_path(self, vm_name: str, snapshot_name: str) -> str:
        """Compute the memory-file path for a new external snapshot.

        Follows the same convention as ``force_create_snapshot`` in
        ``vm/lib/libprovision.sh``:  the memory file lives alongside the
        VM's disk image.
        """
        disk_path = self._get_disk_path(vm_name)
        return os.path.join(
            os.path.dirname(disk_path),
            f"{vm_name}-{snapshot_name}.mem",
        )

    def _get_disk_path(self, vm_name: str) -> str:
        """Return the file path of the VM's primary disk image."""
        process = ExecUtils.run(
            ["virsh", "dumpxml", vm_name],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        root = ET.fromstring(process.stdout)
        for disk in root.findall('.//disk'):
            if disk.get('device') == 'disk':
                source = disk.find('source')
                if source is not None:
                    path = source.get('file', '')
                    if path:
                        return path
        raise RuntimeError(
            f"Could not find disk source path for VM '{vm_name}'"
        )
