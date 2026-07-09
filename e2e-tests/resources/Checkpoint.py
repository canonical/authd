"""Checkpoint coordination library for the authd e2e tests.

A checkpoint is a named VM snapshot representing a specific, expensive-to-reach
state (e.g. the state after the first remote-user QR-code device-authentication
login has completed).  Multiple test cases can share a checkpoint: the first one
to need it creates and saves the snapshot; all subsequent ones restore directly
from the snapshot, skipping the expensive steps.

State machine per checkpoint (broker + release + name):

    not_started  →  in_progress  →  available
                                  ↘  failed

Transitions are made atomic by a per-checkpoint POSIX file lock so that
parallel robot invocations (different brokers/releases sharing the same host)
cannot race.  Within a single sequential robot run no locking is actually needed,
but the file lock is cheap and makes the code correct under both scenarios.

State is written to files under ${XDG_RUNTIME_DIR}/authd-e2e-checkpoints/,
keyed by <broker>-<release>-<name>.  Because XDG_RUNTIME_DIR is cleared on
logout/reboot, the state resets automatically between sessions; the virsh
snapshot itself persists until explicitly deleted.
"""

import fcntl
import os
import time

from robot.api import logger
from robot.api.deco import keyword, library
from robot.libraries.BuiltIn import BuiltIn

from robot.api.types import KeywordName

_CHECKPOINT_DIR = os.path.join(
    os.environ.get('XDG_RUNTIME_DIR', '/tmp'),
    'authd-e2e-checkpoints',
)

_NOT_STARTED = 'not_started'
_IN_PROGRESS = 'in_progress'
_AVAILABLE = 'available'
_FAILED = 'failed'


@library
class Checkpoint:
    """Robot Framework library for managing shared test-checkpoint state."""

    def _key(self, name: str) -> str:
        broker = os.environ.get('BROKER', 'unknown')
        release = os.environ.get('RELEASE', 'unknown')
        return f"{broker}-{release}-{name}"

    def _ensure_dir(self) -> None:
        os.makedirs(_CHECKPOINT_DIR, exist_ok=True)

    def _status_path(self, name: str) -> str:
        self._ensure_dir()
        return os.path.join(_CHECKPOINT_DIR, f"{self._key(name)}.status")

    def _lock_path(self, name: str) -> str:
        self._ensure_dir()
        return os.path.join(_CHECKPOINT_DIR, f"{self._key(name)}.lock")

    def _read_status(self, name: str) -> str:
        try:
            with open(self._status_path(name)) as f:
                return f.read().strip() or _NOT_STARTED
        except FileNotFoundError:
            return _NOT_STARTED

    def _write_status(self, name: str, status: str) -> None:
        with open(self._status_path(name), 'w') as f:
            f.write(status)

    @keyword
    async def claim_checkpoint(self, name: str) -> str:
        """Atomically claim ownership of creating a checkpoint.

        Returns one of:

        - ``'claimed'``:      this caller must create the checkpoint snapshot.
        - ``'available'``:    a snapshot already exists; caller should restore it.
        - ``'in_progress'``:  another caller is creating it; use
                              `Wait For Checkpoint` before restoring.
        - ``'failed'``:       a previous creation attempt failed.
        """
        with open(self._lock_path(name), 'w') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            status = self._read_status(name)
            if status == _NOT_STARTED:
                self._write_status(name, _IN_PROGRESS)
                logger.info(f"Checkpoint '{name}': claimed for creation")
                return 'claimed'
        logger.info(f"Checkpoint '{name}': status is '{status}'")
        return status

    @keyword
    async def mark_checkpoint_available(self, name: str) -> None:
        """Mark the checkpoint snapshot as successfully created."""
        self._write_status(name, _AVAILABLE)
        logger.info(f"Checkpoint '{name}': marked available")

    @keyword
    async def mark_checkpoint_failed(self, name: str) -> None:
        """Mark the checkpoint creation as failed."""
        self._write_status(name, _FAILED)
        logger.info(f"Checkpoint '{name}': marked failed")

    @keyword
    async def wait_for_checkpoint(self, name: str, timeout: int = 600) -> str:
        """Wait until a checkpoint's status is no longer ``in_progress``.

        Polls every 5 seconds until the status transitions to ``'available'``
        or ``'failed'``, then returns that status.  Raises ``RuntimeError``
        if the timeout expires before the status resolves.
        """
        deadline = time.time() + int(timeout)
        while time.time() < deadline:
            status = self._read_status(name)
            if status != _IN_PROGRESS:
                logger.info(
                    f"Checkpoint '{name}': resolved with status '{status}'"
                )
                return status
            logger.info(f"Checkpoint '{name}': still in_progress, waiting…")
            time.sleep(5)
        raise RuntimeError(
            f"Timed out after {timeout}s waiting for checkpoint '{name}'"
        )

    @keyword
    def get_snapshot_name(self, name: str) -> str:
        """Return the virsh snapshot name for a given checkpoint.

        The snapshot is named ``<broker>-checkpoint-<name>`` to distinguish
        checkpoint snapshots from the provisioning snapshots (``<broker>-installed``
        etc.) and to make cleanup easy.
        """
        broker = os.environ.get('BROKER', 'unknown')
        return f"{broker}-checkpoint-{name}"

    @keyword
    def setup_checkpoint(self, name: str, setup_keyword: KeywordName) -> None:
        """Run the full checkpoint state-machine for *name*.

        Determines whether the checkpoint snapshot already exists and, if so,
        restores it directly.  Otherwise, claims ownership of creating it,
        runs *setup_keyword* to reach the desired VM state, saves the snapshot,
        and marks the checkpoint available — or, if a concurrent runner is
        already doing that, waits for it to finish before restoring.

        In all cases, when this keyword returns the VM has been reverted to
        the checkpoint snapshot and the journal and VNC recording have been
        started.

        Args:
            name:          Logical checkpoint name, e.g. ``'authd-user-created'``.
            setup_keyword: Robot Framework keyword (no arguments) that drives
                           the VM from the ``%{BROKER}-installed`` base snapshot
                           to the desired checkpoint state.
        """
        builtin = BuiltIn()
        broker = os.environ.get('BROKER', 'unknown')
        snapshot = self.get_snapshot_name(name)

        snapshot_exists = builtin.run_keyword('Snapshot.Exists', snapshot)
        if snapshot_exists:
            self._restore_and_start(builtin, snapshot)
            return

        status = builtin.run_keyword('Checkpoint.Claim Checkpoint', name)
        if status == 'claimed':
            self._restore_and_start(builtin, f'{broker}-installed')
            try:
                builtin.run_keyword(setup_keyword)
                # Stop receiving journal from the VM before snapshotting:
                # a virsh memory snapshot captures the VM's TCP state, so an
                # active socat connection on port 55000 ends up ESTABLISHED in
                # the snapshot.  On restore the VM would reject new connections
                # until its TCP stack times out the stale entry, causing
                # "Connection refused" in the first consumer test.
                builtin.run_keyword('Journal.Stop Receiving Journal')
                builtin.run_keyword('VNCRecorder.Stop Recording')
                builtin.run_keyword('Snapshot.Create', snapshot)
                builtin.run_keyword('Checkpoint.Mark Checkpoint Available', name)
            except Exception:
                builtin.run_keyword('Checkpoint.Mark Checkpoint Failed', name)
                raise
            # Restart journal and recording for the creator test's body.
            builtin.run_keyword('Journal.Start Receiving Journal')
            builtin.run_keyword('VNCRecorder.Start Recording')
        elif status == 'in_progress':
            resolved = builtin.run_keyword(
                'Checkpoint.Wait For Checkpoint', name
            )
            if resolved != 'available':
                builtin.run_keyword('BuiltIn.Set Tags', 'checkpoint-failed')
                raise RuntimeError(
                    f"Checkpoint '{name}' failed in a concurrent test; "
                    "re-run the suite to retry."
                )
            self._restore_and_start(builtin, snapshot)
        elif status == 'available':
            self._restore_and_start(builtin, snapshot)
        else:
            builtin.run_keyword('BuiltIn.Set Tags', 'checkpoint-failed')
            raise RuntimeError(
                f"Checkpoint '{name}' previously failed; "
                "re-run the suite to retry."
            )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _restore_and_start(self, builtin: BuiltIn, snapshot: str) -> None:
        """Restore *snapshot* and start journal + VNC recording."""
        builtin.run_keyword('utils.Restore Snapshot', snapshot)
        builtin.run_keyword('Journal.Start Receiving Journal')
        builtin.run_keyword('VNCRecorder.Start Recording')
