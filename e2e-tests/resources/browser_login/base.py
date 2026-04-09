"""Shared browser login scaffolding for all broker-specific login scripts.

Each broker provides a ``login()`` function that drives the browser through
its identity provider's device-code flow.  The boilerplate around argument
parsing, GTK initialisation, TLS-error retry and video recording lives here
so it only needs to be maintained once.

Usage from a broker-specific script::

    from .base import run_browser_login
    def login(browser, username, password, device_code, totp_secret, screenshot_dir):
        ...
    if __name__ == "__main__":
        run_browser_login(login)
"""

import argparse
import locale
import logging
import os
import sys
from typing import Callable

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")

from gi.repository import Gtk  # type: ignore

from browser_window import BrowserWindow, ascii_string_to_key_events  # noqa: F401
from generate_totp import generate_totp  # noqa: F401

logging.basicConfig(format="%(asctime)s %(levelname)s: %(message)s", datefmt="%H:%M:%S", level=logging.INFO)
logger = logging.getLogger(__name__)


LoginFunc = Callable[
    # (browser, username, password, device_code, totp_secret, screenshot_dir)
    ..., None
]


def run_browser_login(login_func: LoginFunc) -> None:
    """Parse arguments, set up GTK and a BrowserWindow, then delegate to
    *login_func* for the broker-specific part of the device-code flow.
    *login_func* is called as
    ``login_func(browser, username, password, device_code, totp_secret, screenshot_dir)``
    and should drive the web view through the identity provider pages.
    """
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
            login_func(browser, username, password, args.device_code, totp_secret, screenshot_dir)
        except TimeoutError as e:
            # Sometimes the page can't be loaded due to TLS errors, retry once.
            if not retried_tls_error:
                try:
                    browser.wait_for_pattern("Unacceptable TLS certificate", timeout_ms=1000)
                    repeat = True
                    retried_tls_error = True
                except TimeoutError:
                    pass
            if not repeat:
                raise e
        finally:
            if browser.get_mapped():
                browser.capture_snapshot(screenshot_dir, "failure")
            logger.info("Stopping recording and closing browser")
            browser.stop_recording(os.path.join(args.output_dir, "Webview_Recording.mp4"))
            browser.destroy()
