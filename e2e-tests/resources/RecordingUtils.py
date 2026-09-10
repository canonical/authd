import re

from robot.libraries.BuiltIn import BuiltIn


def current_test_name_for_filename() -> str:
    """Return the current test name in a form that is safe for file names."""
    test_name = BuiltIn().get_variable_value("${TEST_NAME}")
    if not test_name:
        raise RuntimeError("TEST_NAME is not available while recording a test")

    safe_name = re.sub(r"[^A-Za-z0-9.-]+", "_", str(test_name)).strip("._-")
    return safe_name or "test"


def current_test_recording_suffix() -> str:
    return f"{current_test_name_for_filename()}.mp4"


def recording_filename(prefix: str) -> str:
    return f"{prefix}_{current_test_recording_suffix()}"
