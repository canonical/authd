"""Robot Framework library for generating TOTP codes."""

import os

from generate_totp import generate_totp

from robot.api.deco import keyword, library


@library
class TOTP:
    """Generates time-based one-time passwords (TOTP) for use in e2e tests."""

    @keyword
    def generate_totp_code(self) -> str:
        """Return the current TOTP code derived from the TOTP_SECRET environment variable.

        Waits until there are at least 5 seconds left in the current time window
        before generating the code (see generate_totp.py) so the code remains
        valid long enough to be typed in.
        """
        secret = os.environ.get("TOTP_SECRET", "")
        if not secret:
            raise ValueError("TOTP_SECRET environment variable is not set")
        return generate_totp(secret)
