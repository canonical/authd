import sys
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from cleanup_oci_artifacts import is_deletion_candidate  # noqa: E402, I001


NOW = datetime(2026, 10, 8, 12, tzinfo=timezone.utc)
CUTOFF = NOW - timedelta(days=7)


def timestamp(value: datetime) -> str:
    return value.isoformat().replace("+00:00", "Z")


def version(
    created_at: datetime, tags: list[str], updated_at: datetime | None = None
) -> dict:
    return {
        "created_at": timestamp(created_at),
        "updated_at": timestamp(updated_at if updated_at is not None else created_at),
        "metadata": {"container": {"tags": tags}},
    }


class CleanupSelectionTest(unittest.TestCase):
    def test_deletes_versions_older_than_the_retention_window(self):
        self.assertTrue(
            is_deletion_candidate(
                version(NOW - timedelta(days=8), ["sha-tag"]),
                CUTOFF,
            )
        )

    def test_keeps_versions_created_within_the_retention_window(self):
        self.assertFalse(
            is_deletion_candidate(
                version(NOW - timedelta(days=6), ["sha-tag"]),
                CUTOFF,
            )
        )

    def test_keeps_old_versions_that_were_recently_tagged(self):
        self.assertFalse(
            is_deletion_candidate(
                version(
                    NOW - timedelta(days=8),
                    ["current-sha-tag"],
                    updated_at=NOW - timedelta(days=1),
                ),
                CUTOFF,
            )
        )

    def test_keeps_versions_at_the_retention_cutoff(self):
        self.assertFalse(is_deletion_candidate(version(CUTOFF, []), CUTOFF))


if __name__ == "__main__":
    unittest.main()
