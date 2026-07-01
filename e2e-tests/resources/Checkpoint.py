"""Reusable VM checkpoints for sequential Robot tests.

run-tests.sh holds a per-VM lock for the whole run, including startup and
shutdown. Different VMs have independent snapshots. Failed setup attempts are
remembered only for this Robot run, so a new run can retry them.
"""

import hashlib
import json
import os

from robot.api import logger
from robot.api.deco import keyword, library
from robot.libraries.BuiltIn import BuiltIn

import VMUtils


@library(scope='GLOBAL')
class Checkpoint:
    """Restore a checkpoint or create it once from its selected base."""

    def __init__(self) -> None:
        self._failed: set[tuple[str, str]] = set()

    @keyword
    def get_snapshot_name(
        self, name: str, base_snapshot: str, setup_keyword: str
    ) -> str:
        """Key a checkpoint by its base revision and login settings."""
        builtin = BuiltIn()
        identity = builtin.run_keyword('Snapshot.Get Identity', base_snapshot)
        inputs = [
            identity,
            os.environ['E2E_USER'],
            builtin.get_variable_value('${local_password}'),
            setup_keyword,
        ]
        digest = hashlib.sha256(json.dumps(inputs).encode()).hexdigest()[:16]
        return f"{os.environ['BROKER']}-checkpoint-{name}-{digest}"

    @keyword
    def setup_checkpoint(
        self, name: str, setup_keyword: str, base_snapshot: str = ''
    ) -> None:
        """Restore a saved checkpoint, or run setup and save it.

        E2E_TEST_SNAPSHOT overrides only the base used to create a checkpoint.
        A failed creation fails its dependent tests for this run, without
        preventing the next run from retrying. Delete the checkpoint snapshot
        to force setup to run again with unchanged inputs.
        """
        builtin = BuiltIn()
        base_snapshot = (
            os.environ.get('E2E_TEST_SNAPSHOT')
            or base_snapshot
            or f"{os.environ['BROKER']}-installed"
        )
        snapshot = self.get_snapshot_name(name, base_snapshot, setup_keyword)
        key = (VMUtils.vm_name(), snapshot)

        if key in self._failed:
            raise RuntimeError(
                f"Checkpoint '{name}' failed earlier in this run; "
                "re-run the suite to retry."
            )

        if builtin.run_keyword('Snapshot.Exists', snapshot):
            self._restore_and_start(builtin, snapshot, base_snapshot)
            return

        try:
            self._restore_and_start(builtin, base_snapshot, base_snapshot)
            builtin.run_keyword(setup_keyword)
            # Do not save live journal/recording connections in the VM's
            # memory: restored connections would refer to stale host sockets.
            builtin.run_keyword('Journal.Stop Receiving Journal')
            builtin.run_keyword('VNCRecorder.Stop Recording')
            builtin.run_keyword('Snapshot.Create', snapshot)
        except Exception:
            self._failed.add(key)
            logger.error(f"Checkpoint '{name}': creation failed")
            raise

        builtin.run_keyword('Journal.Start Receiving Journal')
        builtin.run_keyword('VNCRecorder.Start Recording')

    def _restore_and_start(
        self, builtin: BuiltIn, snapshot: str, base_snapshot: str
    ) -> None:
        """Restore exactly the selected snapshot and start test logging."""
        builtin.run_keyword('Snapshot.Restore', snapshot)
        builtin.run_keyword('utils.Wait Until System Time Synced')
        builtin.run_keyword('utils.Log Installed Package Information', base_snapshot)
        builtin.run_keyword('Journal.Start Receiving Journal')
        builtin.run_keyword('VNCRecorder.Start Recording')
