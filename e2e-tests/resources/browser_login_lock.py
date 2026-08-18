"""Distributed lock used to serialize browser-based identity-provider logins."""

from __future__ import annotations

import contextlib
import datetime
import json
import logging
import os
import subprocess
import tempfile
import time
import uuid
from dataclasses import dataclass
from typing import Any

from typing_extensions import Self

logger = logging.getLogger(__name__)


class BrowserLoginLockError(RuntimeError):
    """Raised when the shared browser-login lock cannot be used."""


@dataclass(frozen=True)
class BrowserLoginLockConfig:
    bucket: str
    key: str
    endpoint: str
    region: str
    wait_timeout_s: float
    lease_timeout_s: float
    poll_interval_s: float

    @classmethod
    def from_environment(cls) -> BrowserLoginLockConfig | None:
        """Read the lock configuration, or disable locking when unset."""
        names = {
            "bucket": "E2E_BROWSER_LOGIN_LOCK_BUCKET",
            "key": "E2E_BROWSER_LOGIN_LOCK_KEY",
            "endpoint": "E2E_BROWSER_LOGIN_LOCK_ENDPOINT",
        }
        values = {name: os.getenv(environment_name) for name, environment_name in names.items()}
        configured = [value is not None for value in values.values()]
        if not any(configured):
            return None
        if not all(configured):
            missing = [
                environment_name
                for name, environment_name in names.items()
                if values[name] is None
            ]
            raise BrowserLoginLockError(
                "Browser-login locking is partially configured; missing "
                + ", ".join(missing)
            )

        return cls(
            bucket=values["bucket"],
            key=values["key"],
            endpoint=values["endpoint"],
            region=os.getenv("E2E_BROWSER_LOGIN_LOCK_REGION", "auto"),
            wait_timeout_s=_positive_duration(
                "E2E_BROWSER_LOGIN_LOCK_WAIT_TIMEOUT_S", default=30 * 60
            ),
            lease_timeout_s=_positive_duration(
                "E2E_BROWSER_LOGIN_LOCK_LEASE_TIMEOUT_S", default=30 * 60
            ),
            poll_interval_s=_positive_duration(
                "E2E_BROWSER_LOGIN_LOCK_POLL_INTERVAL_S", default=10
            ),
        )


def _positive_duration(name: str, default: float) -> float:
    value = os.getenv(name)
    if value is None:
        return default
    try:
        duration = float(value)
    except ValueError as e:
        raise BrowserLoginLockError(f"{name} must be a positive number") from e
    if duration <= 0:
        raise BrowserLoginLockError(f"{name} must be a positive number")
    return duration


@dataclass(frozen=True)
class _LockMetadata:
    etag: str
    last_modified: float


class BrowserLoginLock:
    """An S3-compatible lease lock backed by a conditional object."""

    def __init__(self, config: BrowserLoginLockConfig):
        self._config = config
        self._etag: str | None = None
        self._token = uuid.uuid4().hex

    def __enter__(self) -> Self:
        self.acquire()
        return self

    def __exit__(self, exception_type, exception, traceback) -> bool:
        try:
            self.release()
        except BrowserLoginLockError:
            if exception_type is None:
                raise
            logger.exception("Could not release the browser-login lock")
        return False

    def acquire(self) -> None:
        """Wait for and acquire the lock, recovering an expired lease."""
        if self._etag is not None:
            raise BrowserLoginLockError("The browser-login lock is already acquired")

        deadline = time.monotonic() + self._config.wait_timeout_s
        with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as token_file:
            token_file.write(self._token)
            token_path = token_file.name

        try:
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise BrowserLoginLockError(
                        "Timed out waiting for the shared browser-login lock"
                    )

                if self._try_create(token_path):
                    logger.info("Acquired the shared browser-login lock")
                    return

                metadata = self._get_metadata()
                if metadata is None:
                    continue

                age = time.time() - metadata.last_modified
                if age >= self._config.lease_timeout_s:
                    logger.warning(
                        "Recovering a stale browser-login lock that is %.0fs old", age
                    )
                    self._try_delete(metadata.etag)
                    continue

                logger.info(
                    "Another browser login holds the shared lock; waiting %.0fs",
                    min(self._config.poll_interval_s, remaining),
                )
                time.sleep(min(self._config.poll_interval_s, remaining))
        finally:
            os.unlink(token_path)

    def release(self) -> None:
        """Release the lock only if the object is still owned by this instance."""
        if self._etag is None:
            return

        etag = self._etag
        if not self._try_delete(etag):
            raise BrowserLoginLockError(
                "The browser-login lock changed before it could be released"
            )

        self._etag = None
        logger.info("Released the shared browser-login lock")

    def _try_create(self, token_path: str) -> bool:
        result = self._run_aws(
            "put-object",
            "--body",
            token_path,
            "--if-none-match",
            "*",
        )
        if result.returncode != 0:
            if _is_precondition_failure(result):
                return False
            raise _aws_error("put-object", result)

        response = _parse_response("put-object", result)
        etag = _normalise_etag(response.get("ETag"))
        if etag is None:
            raise BrowserLoginLockError(
                "The browser-login lock was created without an object ETag"
            )
        self._etag = etag
        return True

    def _get_metadata(self) -> _LockMetadata | None:
        result = self._run_aws("head-object")
        if result.returncode != 0:
            if _is_not_found(result):
                return None
            raise _aws_error("head-object", result)

        response = _parse_response("head-object", result)
        etag = _normalise_etag(response.get("ETag"))
        last_modified = response.get("LastModified")
        if etag is None or not isinstance(last_modified, str):
            raise BrowserLoginLockError(
                "The browser-login lock object has incomplete metadata"
            )
        try:
            modified_at = datetime.datetime.fromisoformat(
                last_modified.replace("Z", "+00:00")
            ).timestamp()
        except ValueError as e:
            raise BrowserLoginLockError(
                f"Could not parse browser-login lock timestamp {last_modified!r}"
            ) from e
        return _LockMetadata(etag=etag, last_modified=modified_at)

    def _try_delete(self, etag: str) -> bool:
        # R2 supports conditional PutObject, but not a conditional
        # DeleteObject. First replace the object only if this lease is still
        # ours, then delete the replacement. A competing owner cannot acquire
        # the key between those operations because it still exists.
        with tempfile.NamedTemporaryFile("w", encoding="utf-8") as token_file:
            token_file.write(uuid.uuid4().hex)
            token_file.flush()
            result = self._run_aws(
                "put-object",
                "--body",
                token_file.name,
                "--if-match",
                etag,
            )
            if result.returncode != 0:
                if _is_precondition_failure(result):
                    return False
                raise _aws_error("put-object", result)

        result = self._run_aws("delete-object")
        if result.returncode != 0:
            raise _aws_error("delete-object", result)
        return True

    def _run_aws(self, operation: str, *arguments: str) -> subprocess.CompletedProcess[str]:
        command = [
            "aws",
            "s3api",
            operation,
            "--bucket",
            self._config.bucket,
            "--key",
            self._config.key,
            "--endpoint-url",
            self._config.endpoint,
            "--region",
            self._config.region,
            "--output",
            "json",
            *arguments,
        ]
        environment = os.environ.copy()
        environment["AWS_PAGER"] = ""
        try:
            return subprocess.run(
                command,
                capture_output=True,
                text=True,
                check=False,
                env=environment,
            )
        except FileNotFoundError as e:
            raise BrowserLoginLockError(
                "The browser-login lock is enabled, but the aws command is unavailable"
            ) from e


def _parse_response(
    operation: str, result: subprocess.CompletedProcess[str]
) -> dict[str, Any]:
    try:
        response = json.loads(result.stdout)
    except json.JSONDecodeError as e:
        raise BrowserLoginLockError(
            f"aws {operation} returned invalid JSON"
        ) from e
    if not isinstance(response, dict):
        raise BrowserLoginLockError(f"aws {operation} returned a non-object response")
    return response


def _normalise_etag(value: Any) -> str | None:
    if not isinstance(value, str) or not value:
        return None
    # Preserve the quotes returned by S3: they are part of the If-Match
    # entity-tag value sent back on the conditional PutObject request.
    return value.strip()


def _is_precondition_failure(result: subprocess.CompletedProcess[str]) -> bool:
    output = f"{result.stdout}\n{result.stderr}".lower()
    return "preconditionfailed" in output or "412" in output


def _is_not_found(result: subprocess.CompletedProcess[str]) -> bool:
    output = f"{result.stdout}\n{result.stderr}".lower()
    return (
        "notfound" in output
        or "nosuchkey" in output
        or "404" in output
    )


def _aws_error(operation: str, result: subprocess.CompletedProcess[str]) -> BrowserLoginLockError:
    details = (result.stderr or result.stdout).strip()
    if len(details) > 500:
        details = details[:500] + "..."
    return BrowserLoginLockError(
        f"aws {operation} failed with exit code {result.returncode}: {details}"
    )


def get_browser_login_lock() -> contextlib.AbstractContextManager[None]:
    """Return the configured lock, or a no-op context for local runs."""
    config = BrowserLoginLockConfig.from_environment()
    if config is None:
        return contextlib.nullcontext()
    return BrowserLoginLock(config)
