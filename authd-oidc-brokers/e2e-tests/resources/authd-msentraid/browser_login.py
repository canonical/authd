#!/usr/bin/env python3

import gi, os, sys

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
from browser_window import BrowserWindow, ascii_string_to_key_events  # type: ignore # This is resolved at runtime


if __name__ == "__main__":
    if os.getenv("SHOW_WEBVIEW") is None and "RUNNING_OFFSCREEN" not in os.environ:
        os.execv(
            "/usr/bin/env",
            [
                "/usr/bin/env",
                "RUNNING_OFFSCREEN=1",
                "GDK_BACKEND=x11",
                "xvfb-run",
                "-a",
                sys.executable,
            ]
            + sys.argv,
        )

    if len(sys.argv) < 4:
        print("Usage: BrowserWindow.py <username> <password> <code>")
        sys.exit(1)

    username = sys.argv[1]
    password = sys.argv[2]
    device_code = sys.argv[3]

    Gtk.init(None)
    browser = BrowserWindow()
    browser.show_all()

    browser.web_view.load_uri("https://microsoft.com/devicelogin")
    browser.wait_for_stable_page()

    browser.send_key_taps(ascii_string_to_key_events(device_code) + [Gdk.KEY_Return])
    browser.wait_for_stable_page()

    browser.send_key_taps(ascii_string_to_key_events(username) + [Gdk.KEY_Return])
    browser.wait_for_stable_page()

    browser.send_key_taps(ascii_string_to_key_events(password) + [Gdk.KEY_Return])
    browser.wait_for_stable_page()

    browser.send_key_taps([Gdk.KEY_Return])
    browser.wait_for_stable_page()
