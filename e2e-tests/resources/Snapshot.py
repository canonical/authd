import hashlib
import os
import subprocess
import uuid
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

        Disk and memory files have unique names alongside the VM's disk image,
        so files left by failed or deleted snapshots cannot block recreation.

        Args:
            name: The name for the new snapshot.
        """
        vm_name = VMUtils.vm_name()
        disk_path, disk_target = self._get_disk(vm_name)
        prefix = os.path.join(
            os.path.dirname(disk_path), f"{vm_name}-{name}.{uuid.uuid4().hex}"
        )
        mem_file = f"{prefix}.mem"
        logger.info(f"Creating snapshot '{name}', memory file: {mem_file}")
        process = ExecUtils.run(
            ["virsh", "snapshot-create-as", vm_name, name,
             "--diskspec", f"{disk_target},file={prefix}.qcow2,snapshot=external",
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
            ["virsh", "snapshot-list", vm_name, "--name"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        return name in process.stdout.splitlines()

    @keyword
    async def get_identity(self, name: str) -> str:
        """Identify a base revision by its creation time, disks and domain."""
        process = ExecUtils.run(
            ["virsh", "snapshot-dumpxml", VMUtils.vm_name(), name],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        return hashlib.sha256(process.stdout.encode()).hexdigest()

    def _get_disk(self, vm_name: str) -> tuple[str, str]:
        """Return the file path and target of the VM's primary disk."""
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
                target = disk.find('target')
                if source is not None and target is not None:
                    path = source.get('file', '')
                    device = target.get('dev', '')
                    if path and device:
                        return path, device
        raise RuntimeError(
            f"Could not find disk source path for VM '{vm_name}'"
        )
