"""Robot Framework library for generating TOTP codes."""

import os

from generate_totp import generate_totp
from robot.api.deco import keyword, library


@library
class TOTP:
    """Generates time-based one-time passwords (TOTP) for use in e2e tests."""

    @keyword
    def generate_totp_code(
        self, secret_env: str = "TOTP_SECRET", previous_code: str = ""
    ) -> str:
        """Return a code for the secret in the environment variable secret_env.

        Waits until there are at least 5 seconds left in the current time window
        before generating each code (see generate_totp.py).
        """
        secret = os.environ.get(secret_env, "")
        if not secret:
            raise ValueError(f"{secret_env} environment variable is not set")
        return generate_totp(secret, previous_code)
