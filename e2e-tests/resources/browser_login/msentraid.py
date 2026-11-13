#!/usr/bin/env python3
"""Browser-based device-code login flow for Microsoft Entra ID."""

import os
import re
import sys

import gi  # noqa: E402

gi.require_version("Gdk", "3.0")
from gi.repository import Gdk  # type: ignore  # noqa: E402

# Allow imports from this package when executed as a script.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from base import (
    generate_totp,
    logger,
    run_browser_login,
)

# innerText can put a line break or a non-breaking space inside the heading.
APPROVE_PAGE_PATTERN = r"Approve\s+sign\s*-\s*in\s+on\s+another\s+device"


def page_text(matches: list[str]) -> str:
    """Collapse the whitespace that innerText inserts at line breaks."""
    return " ".join(" ".join(matches).split())


def click_approve(browser) -> None:
    """Activate the Approve button on the "Approve sign-in on another device" page.

    Pressing Return is unsafe there: the focused button is "This wasn't me",
    which declines the sign-in.
    """
    result = browser.run_javascript_sync(
        """(function() {
            var nodes = document.querySelectorAll(
                'button, input[type=button], input[type=submit], [role=button], a'
            );
            for (var i = 0; i < nodes.length; i++) {
                var label = (nodes[i].innerText || nodes[i].value || '').trim();
                if (label === 'Approve') {
                    nodes[i].click();
                    return 'clicked Approve';
                }
            }
            return 'Approve button not found';
        })()"""
    )
    logger.info(result)
    if result != "clicked Approve":
        raise RuntimeError(result)


def login(
    browser,
    username: str,
    password: str,
    device_code: str,
    totp_secret: str,
    screenshot_dir: str = ".",
):
    url = "https://login.microsoft.com/device"
    logger.info(f"Loading URL: {url}")
    browser.web_view.load_uri(url)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "page-loaded")

    browser.wait_for_pattern("Enter code to allow access")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-code")
    browser.send_text(device_code)
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("Sign in", timeout_ms=30000)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-username")
    browser.send_text(username)
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("Enter password")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-password")
    browser.send_text(password, secret=True)
    browser.send_key_taps([Gdk.KEY_Return])

    # Entra can show a TOTP prompt and then either the old confirmation page or
    # the newer approval page.
    matches = browser.wait_for_pattern(
        rf"(Enter code|Are you trying to sign in|{APPROVE_PAGE_PATTERN})",
        timeout_ms=30000,
    )
    browser.wait_for_stable_page()
    pages = page_text(matches)
    if "Enter code" in pages:
        browser.capture_snapshot(screenshot_dir, "device-login-enter-totp-code")
        browser.send_text(generate_totp(totp_secret), secret=True)
        browser.send_key_taps([Gdk.KEY_Return])
        matches = browser.wait_for_pattern(
            rf"(Are you trying to sign in|{APPROVE_PAGE_PATTERN})",
            timeout_ms=30000,
        )
        browser.wait_for_stable_page()
        pages = page_text(matches)

    if re.search(APPROVE_PAGE_PATTERN, pages):
        browser.capture_snapshot(screenshot_dir, "device-login-approve-signin")
        click_approve(browser)
    else:
        browser.capture_snapshot(screenshot_dir, "device-login-confirm-signin")
        browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("You have signed in")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-success")


if __name__ == "__main__":
    run_browser_login(login)
