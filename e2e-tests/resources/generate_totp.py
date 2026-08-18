#!/usr/bin/env python3

import argparse
import base64
import dataclasses
import hashlib
import hmac
import struct
import time

# Length of a TOTP time step, as defined by RFC 6238 and used by both Google
# and Microsoft Entra ID.
TIME_STEP_S = 30

# Minimum validity a freshly generated code must have left. Generating a code
# shortly before its time step ends leaves too little time to type it in and
# have the identity provider validate it, so we wait for the next step instead.
MIN_VALIDITY_S = 10

TOTP_CODE_LENGTH = 6


@dataclasses.dataclass(frozen=True)
class TOTPCode:
    """A TOTP code together with the timing information it was derived from."""

    code: str
    time_step: int
    generated_at: float

    def seconds_left(self, now: float | None = None) -> float:
        """Return for how much longer this code is valid, in seconds."""
        if now is None:
            now = time.time()
        return (self.time_step + 1) * TIME_STEP_S - now

    def age(self, now: float | None = None) -> float:
        """Return how long ago this code was generated, in seconds."""
        if now is None:
            now = time.time()
        return now - self.generated_at


def totp_for_time_step(secret: str, time_step: int) -> str:
    """Derive the TOTP code for `time_step` from `secret`."""
    key = base64.b32decode(secret, True)
    msg = struct.pack(">Q", time_step)
    hashed_obj = hmac.new(key, msg, hashlib.sha1).digest()
    o = hashed_obj[19] & 15

    totp_code = (struct.unpack(">I", hashed_obj[o:o + 4])[0] & 0x7fffffff) % 1000000

    return f"{totp_code:0{TOTP_CODE_LENGTH}d}"


def generate_totp_details(secret: str) -> TOTPCode:
    """Generate a TOTP code and return it along with its timing information.

    Waits until the code is valid for at least MIN_VALIDITY_S seconds.
    """
    # The code is generated from the current time and is only valid until the
    # end of its time step. This means that if we generate the code just before
    # the time step ends, it might already be invalid by the time the identity
    # provider validates it, so we wait for the next step in that case.
    while TIME_STEP_S - time.time() % TIME_STEP_S < MIN_VALIDITY_S:
        time.sleep(0.1)

    generated_at = time.time()
    time_step = int(generated_at) // TIME_STEP_S

    return TOTPCode(code=totp_for_time_step(secret, time_step),
                    time_step=time_step,
                    generated_at=generated_at)


def generate_totp(secret: str) -> str:
    return generate_totp_details(secret).code

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("totp_secret")
    args = parser.parse_args()

    print(generate_totp(args.totp_secret))
