#!/usr/bin/env python3

import argparse
import base64
import hashlib
import hmac
import struct
import time

def generate_totp(secret: str) -> str:
    key = base64.b32decode(secret, True)
    msg = struct.pack(">Q", int(time.time()) // 30)
    hashed_obj = hmac.new(key, msg, hashlib.sha1).digest()
    o = hashed_obj[19] & 15

    totp_code = str((struct.unpack(">I", hashed_obj[o:o + 4])[0] & 0x7fffffff) % 1000000)
    while len(totp_code) != 6:
        totp_code += '0'

    return totp_code

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("totp_secret")
    args = parser.parse_args()

    print(generate_totp(args.totp_secret))
