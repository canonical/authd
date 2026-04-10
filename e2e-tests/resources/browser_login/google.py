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

# Patterns that identify each page, used in wait_for_pattern calls.
_ENTER_CODE = "Enter the code displayed on your device"
_SIGN_IN = "Email or phone"
_ENTER_PASSWORD = "Enter your password"
_VERIFY_YOU = "Verify it’s you"
_TWO_STEP = "2-Step Verification"
_PATTERN_ENTER_TOTP = f"{_TWO_STEP}|{_VERIFY_YOU}"
_CHOOSE_ACCOUNT = "Choose an account"
_SIGNING_BACK_IN = "You’re signing back in"
_SUCCESS = "Continue on your device"
_WRONG_TOTP = "Wrong code. Try again."
_WRONG_NUMBER_OF_DIGITS = "Wrong number of digits. Try again."
_PATTERN_WRONG_TOTP = f"{_WRONG_TOTP}|{_WRONG_NUMBER_OF_DIGITS}"
_EMAIL_NOT_FOUND = "Couldn’t find your Google Account"
_EMAIL_INVALID = "Enter a valid email or phone number"
_PATTERN_WRONG_EMAIL = f"{_EMAIL_NOT_FOUND}|{_EMAIL_INVALID}"
_WRONG_PASSWORD = "Wrong password"


class WrongTOTPCodeError(Exception):
    pass


class GoogleLoginFlow:
    """Drives the browser through the Google device-code OAuth flow.

    The flow handles pages in whatever order Google presents them rather than
    assuming a fixed sequence.  After entering the device code, it repeatedly
    checks which page is currently shown and dispatches to the appropriate
    handler until the success page is reached.
    """

    # All patterns that can appear after the device code is submitted.
    _ALL_POST_CODE_PATTERNS = "|".join([
        _SIGN_IN,
        _ENTER_PASSWORD,
        _PATTERN_ENTER_TOTP,
        _CHOOSE_ACCOUNT,
        _SIGNING_BACK_IN,
        _SUCCESS,
        _PATTERN_WRONG_TOTP,
        _PATTERN_WRONG_EMAIL,
        _WRONG_PASSWORD,
    ])

    def __init__(self, browser, username: str, password: str, device_code: str,
                 totp_secret: str, screenshot_dir: str = "."):
        self._browser = browser
        self._username = username
        self._password = password
        self._device_code = device_code
        self._totp_secret = totp_secret
        self._screenshot_dir = screenshot_dir
        self._last_totp_code: str | None = None

    def run(self) -> None:
        """Execute the full login flow, retrying on TOTP failures."""
        num_totp_failures = 0
        while True:
            try:
                self._do_login()
                return
            except WrongTOTPCodeError:
                # Google sometimes rejects the TOTP code for an unclear reason.
                # It's not enough to just re-enter a new code on the current
                # page; we have to restart the whole flow.
                if num_totp_failures == TOTP_CODE_MAX_TRIES:
                    raise RuntimeError(
                        f"Failed to log in: TOTP code was rejected too many "
                        f"times ({TOTP_CODE_MAX_TRIES})")
                logger.info("TOTP code was rejected, retrying with a new code")
                # Wait until a fresh TOTP code is available.
                while generate_totp(self._totp_secret) == self._last_totp_code:
                    time.sleep(1)
                num_totp_failures += 1

    _LOGIN_TIMEOUT_S = 3 * 60  # 3 minutes

    def _do_login(self) -> None:
        """Load the entry URL and process pages until success."""
        url = "https://accounts.google.com/o/oauth2/device/usercode?hl=en&flowName=DeviceOAuth"
        logger.info(f"Loading URL: {url}")
        self._browser.web_view.load_uri(url)
        self._browser.wait_for_stable_page()
        self._browser.capture_snapshot(self._screenshot_dir, "page-loaded")

        self._handle_enter_device_code_page()

        # After the device code is entered, Google can show pages in varying
        # order depending on the session state.  We keep dispatching to the
        # right handler until we reach the success page.
        deadline = time.monotonic() + self._LOGIN_TIMEOUT_S
        while True:
            if time.monotonic() > deadline:
                raise RuntimeError(
                    f"Login flow timed out after {self._LOGIN_TIMEOUT_S} seconds")
            # Stabilize first so that wait_for_pattern reads the new page's
            # text rather than leftovers from the previous one.  This is safe
            # to do before typing because wait_for_stable_page no longer steals
            # focus (it suppresses cursor-blink draw events via CSS instead).
            self._browser.wait_for_stable_page()
            matches = self._browser.wait_for_pattern(
                self._ALL_POST_CODE_PATTERNS, timeout_ms=20000)
            if self._dispatch(matches):
                return  # success

    def _dispatch(self, matches: list[str]) -> bool:
        """Handle the matched page pattern.

        Returns True when the success page has been reached.
        """
        if _SUCCESS in matches:
            self._handle_success_page()
            return True

        # We check the error cases first, because the patterns of the other
        # pages also match in the error cases.
        if _WRONG_TOTP in matches or _WRONG_NUMBER_OF_DIGITS in matches:
            raise WrongTOTPCodeError("TOTP code was rejected")
        if _EMAIL_NOT_FOUND in matches or _EMAIL_INVALID in matches:
            self._handle_wrong_email()
        elif _WRONG_PASSWORD in matches:
            self._handle_wrong_password()
        elif _SIGN_IN in matches:
            self._handle_sign_in_page()
        elif _ENTER_PASSWORD in matches:
            self._handle_enter_password_page()
        elif _TWO_STEP in matches or _VERIFY_YOU in matches:
            self._handle_totp_page()
        elif _CHOOSE_ACCOUNT in matches:
            self._handle_choose_account_page()
        elif _SIGNING_BACK_IN in matches:
            self._handle_signing_back_in_page()
        else:
            raise RuntimeError(f"Unexpected page with patterns: {matches}")
        return False

    # ------------------------------------------------------------------
    # Individual page handlers
    # ------------------------------------------------------------------

    def _handle_enter_device_code_page(self) -> None:
        self._browser.wait_for_pattern(_ENTER_CODE)
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-enter-code")
        self._browser.send_key_taps(
            ascii_string_to_key_events(self._device_code) + [Gdk.KEY_Return])

    def _handle_sign_in_page(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-enter-username")
        self._browser.send_key_taps(
            ascii_string_to_key_events(self._username) + [Gdk.KEY_Return])

    def _handle_enter_password_page(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-enter-password")
        self._browser.send_key_taps(
            ascii_string_to_key_events(self._password) + [Gdk.KEY_Return])

    def _handle_totp_page(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-enter-totp-code")
        self._last_totp_code = generate_totp(self._totp_secret)
        self._browser.send_key_taps(
            ascii_string_to_key_events(self._last_totp_code) + [Gdk.KEY_Return])

    def _handle_choose_account_page(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-choose-account")
        self._browser.send_key_taps([Gdk.KEY_Return])

    def _handle_signing_back_in_page(self) -> None:
        # "You’re signing back in" – click "Continue".
        # Pressing Enter alone is not enough; we must tab to the button first.
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-confirmation")
        self._browser.send_key_taps([Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab, Gdk.KEY_Tab])
        self._browser.send_key_taps([Gdk.KEY_Return])

    def _handle_success_page(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-success")
        logger.info("Successfully logged in")

    def _handle_wrong_email(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-wrong-email")
        self._browser.send_key_taps(len(self._username) * [Gdk.KEY_BackSpace])

    def _handle_wrong_password(self) -> None:
        self._browser.capture_snapshot(self._screenshot_dir, "device-login-wrong-password")
        self._browser.send_key_taps(len(self._password) * [Gdk.KEY_BackSpace])

def login(browser, username: str, password: str, device_code: str, totp_secret: str, screenshot_dir: str = "."):
    GoogleLoginFlow(browser, username, password, device_code, totp_secret, screenshot_dir).run()


if __name__ == "__main__":
    run_browser_login(login)
