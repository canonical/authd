#!/usr/bin/env python3

import argparse
import base64
import hashlib
import hmac
import struct
import time

TIME_WINDOW = 5

def generate_totp(secret: str) -> str:
    # The code is generated according to the current time and is valid for 30 seconds.
    # This means that if we generate the code just before the time window changes,
    # it might be invalid by the time we use it. To avoid this, we make sure the time
    # is safely within a new window before generating the code.
    while time.time() % 30 > (30 - TIME_WINDOW):
        continue

    key = base64.b32decode(secret, True)
    msg = struct.pack(">Q", int(time.time()) // 30)
    hashed_obj = hmac.new(key, msg, hashlib.sha1).digest()
    o = hashed_obj[19] & 15

    totp_code = (struct.unpack(">I", hashed_obj[o:o + 4])[0] & 0x7fffffff) % 1000000

    return f"{totp_code:06d}"

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("totp_secret")
    args = parser.parse_args()

    print(generate_totp(args.totp_secret))
