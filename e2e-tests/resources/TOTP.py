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

        Waits until the code is valid for long enough to be typed in (see
        MIN_VALIDITY_S in generate_totp.py) before generating it.
        """
        secret = os.environ.get("TOTP_SECRET", "")
        if not secret:
            raise ValueError("TOTP_SECRET environment variable is not set")
        return generate_totp(secret)
