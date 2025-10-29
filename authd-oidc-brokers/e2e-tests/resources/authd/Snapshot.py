from robot.api.deco import keyword, library  # type: ignore

VM_NAME="e2e-runner"

@library
class Snapshot:
    @keyword
    async def restore(self, name: str) -> None:
        """
        Revert the VM to the specified snapshot.

        Args:
            name: The name of the snapshot to revert to.
        """
        import subprocess

        subprocess.run(
            ["virsh", "snapshot-revert", VM_NAME, name],
            check=True,
        )
