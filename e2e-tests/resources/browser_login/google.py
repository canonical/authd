#!/usr/bin/env python3
"""Browser-based device-code login flow for Google IAM."""

import os
import sys

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


def login(browser, username: str, password: str, device_code: str, totp_secret: str, screenshot_dir: str = "."):
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

    browser.wait_for_pattern("Sign in", timeout_ms=20000)
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
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-totp-code")
    browser.send_key_taps(
        ascii_string_to_key_events(generate_totp(totp_secret)) + [Gdk.KEY_Return])

    browser.wait_for_pattern("Choose an account")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-choose-account")
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("signing back in", timeout_ms=20000)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-confirmation")
    # Sadly, just pressing Enter is not enough here, we need to tab to the correct button.
    browser.send_key_taps([Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab])
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("Continue on your device")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-success")


if __name__ == "__main__":
    run_browser_login(login)
