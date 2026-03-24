#!/usr/bin/env python3
import argparse
import locale
import logging
import os
import sys

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")

from gi.repository import Gdk, Gtk  # type: ignore # The interpreter usually does not recognize imports from gi.repository

# The import will be resolved at runtime, which means that the directory structure will be something like:
# test_run_dir/
#   resources/
#     authd/
#       browser_window.py <- the dependency
#     authd-msentraid/
#       browser_login.py  <- this file
sys.path.append(os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "authd")))
from browser_window import (
    BrowserWindow,
    ascii_string_to_key_events,
)  # type: ignore # This is resolved at runtime

logging.basicConfig(format='%(asctime)s %(levelname)s: %(message)s', datefmt='%H:%M:%S')
logger = logging.getLogger(__name__)

from generate_totp import generate_totp # type: ignore # This is resolved at runtime

SNAPSHOT_INDEX = 0

def main():
    parser = argparse.ArgumentParser()

    parser.add_argument("device_code")
    parser.add_argument("--output-dir", required=False, default=os.path.realpath(os.curdir))
    parser.add_argument("--show-webview", action="store_true")
    args = parser.parse_args()

    username = os.getenv("E2E_USER")
    password = os.getenv("E2E_PASSWORD")
    totp_secret = os.getenv("TOTP_SECRET")
    if username is None or password is None or totp_secret is None:
        logger.error("E2E_USER, E2E_PASSWORD, and TOTP_SECRET environment variables must be set")
        sys.exit(1)

    locale.setlocale(locale.LC_ALL, "C")

    screenshot_dir = os.path.join(args.output_dir, "webview-snapshots")
    os.makedirs(screenshot_dir, exist_ok=True)

    Gtk.init(None)

    repeat = True
    retried_tls_error = False
    while repeat:
        repeat = False

        browser = BrowserWindow()
        browser.show_all()
        browser.start_recording()

        try:
            login(browser, username, password, args.device_code, totp_secret, screenshot_dir)
        except TimeoutError:
            # Sometimes the page can't be loaded due to TLS errors, retry once
            if not retried_tls_error:
                browser.wait_for_pattern("Unacceptable TLS certificate", timeout_ms=1000)
                repeat = True
                retried_tls_error = True
        finally:
            if browser.get_mapped():
                browser.capture_snapshot(screenshot_dir, "failure")
            browser.stop_recording(os.path.join(args.output_dir, "Webview_Recording.webm"))
            browser.destroy()


def login(browser, username: str, password: str, device_code: str, totp_secret: str, screenshot_dir: str = "."):
    url = "https://login.microsoft.com/device"
    logger.info(f"Loading URL: {url}")
    browser.web_view.load_uri(url)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "page-loaded")

    browser.wait_for_pattern("Enter code to allow access")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-code")
    browser.send_key_taps(
        ascii_string_to_key_events(device_code) + [Gdk.KEY_Return])

    browser.wait_for_pattern("Sign in", timeout_ms=20000)
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-username")
    browser.send_key_taps(
        ascii_string_to_key_events(username) + [Gdk.KEY_Return])

    browser.wait_for_pattern("Enter password")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-enter-password")
    browser.send_key_taps(
        ascii_string_to_key_events(password) + [Gdk.KEY_Return])

    match = browser.wait_for_pattern(r"(Enter code|Are you trying to sign in)")
    browser.wait_for_stable_page()
    if match == "Enter code":
        browser.capture_snapshot(screenshot_dir, "device-login-enter-totp-code")
        browser.send_key_taps(
            ascii_string_to_key_events(generate_totp(totp_secret)) + [Gdk.KEY_Return])
        browser.wait_for_pattern("Are you trying to sign in")
        browser.wait_for_stable_page()

    browser.capture_snapshot(screenshot_dir, "device-login-confirm-signin")
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_pattern("You have signed in")
    browser.wait_for_stable_page()
    browser.capture_snapshot(screenshot_dir, "device-login-success")


if __name__ == "__main__":
    main()
