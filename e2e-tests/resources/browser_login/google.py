#!/usr/bin/env python3
"""Browser-based device-code login flow for Google IAM."""

import os
import sys
import time

import gi  # noqa: E402

gi.require_version("Gdk", "3.0")
from gi.repository import Gdk  # type: ignore  # noqa: E402

# Allow imports from this package when executed as a script.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from base import (
    ascii_string_to_key_events,
    generate_totp,
    logger,
    run_browser_login,
)

TOTP_CODE_MAX_TRIES = 3
totp_code = None


class WrongTOTPCodeError(Exception):
    pass


def login(browser, username: str, password: str, device_code: str, totp_secret: str, screenshot_dir: str = "."):
    num_tries = 0
    while True:
        try:
            try_login(browser, username, password, device_code, totp_secret, screenshot_dir)
            break
        except WrongTOTPCodeError:
            # Google sometimes rejects the TOTP code for an unclear reason. It's
            # not enough to just wait until we get a new TOTP code and enter that
            # on the page. Instead, we have to redo the whole login flow.
            if num_tries == TOTP_CODE_MAX_TRIES:
                raise RuntimeError(
                    f"Failed to log in: TOTP code was rejected too many times ({TOTP_CODE_MAX_TRIES})")
            logger.info("TOTP code was rejected, retrying with a new code")
            # Wait until we get a new TOTP code
            while generate_totp(totp_secret) == totp_code:
                time.sleep(1)
            num_tries += 1

def try_login(browser, username: str, password: str, device_code: str, totp_secret: str, screenshot_dir: str = "."):
    url = "https://accounts.google.com/o/oauth2/device/usercode?hl=en&flowName=DeviceOAuth"
    logger.info(f"Loading URL: {url}")
    browser.web_view.load_uri(url)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "page-loaded")

    browser.wait_for_pattern("Enter the code displayed on your device")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-code")
    browser.send_key_taps(
        ascii_string_to_key_events(device_code) + [Gdk.KEY_Return])

    match = browser.wait_for_pattern("Sign in|Verify it's you", timeout_ms=20000)
    if match == "Sign in":
        browser.wait_for_stable_page()
        browser.capture_snapshot(screenshot_dir, "device-login-enter-username")
        browser.send_key_taps(
            ascii_string_to_key_events(username) + [Gdk.KEY_Return])

        browser.wait_for_pattern("Enter your password")
        browser.wait_for_stable_page()
        browser.capture_snapshot(screenshot_dir, "device-login-enter-password")
        browser.send_key_taps(
            ascii_string_to_key_events(password) + [Gdk.KEY_Return])

        browser.wait_for_pattern("2-Step Verification", timeout_ms=20000)

    # Enter the TOTP code
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-totp-code")
    global totp_code
    totp_code = generate_totp(totp_secret)
    browser.send_key_taps(ascii_string_to_key_events(totp_code) + [Gdk.KEY_Return])

    # Sometimes the "Choose an account" page is automatically skipped,
    # so we also have to check for the "signing back in" page that comes
    # after it.
    match = browser.wait_for_pattern(
        f"Choose an account|signing back in|Wrong code. Try again",
        timeout_ms=20000,
    )
    if match == "Wrong code. Try again":
        raise WrongTOTPCodeError("TOTP code was rejected")
    if match == "Choose an account":
        browser.wait_for_stable_page()
        browser.capture_snapshot(screenshot_dir, "device-login-choose-account")
        browser.send_key_taps([Gdk.KEY_Return])
        browser.wait_for_pattern("signing back in", timeout_ms=20000)

    # Click on "Continue" on the "You're signing back in" page.
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-confirmation")
    # Sadly, just pressing Enter is not enough here, we need to tab to the correct button.
    browser.send_key_taps([Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab])
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("Continue on your device")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-success")
    logger.info("Successfully logged in")


if __name__ == "__main__":
    run_browser_login(login)
