#!/usr/bin/env python3
import argparse
import cairo
import locale
import subprocess

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


SNAPSHOT_INDEX = 0


def main():
    locale.setlocale(locale.LC_ALL, "C")

    parser = argparse.ArgumentParser()

    parser.add_argument("username")
    parser.add_argument("password")
    parser.add_argument("device_code")
    parser.add_argument("--output-dir", required=False, default=os.path.realpath(os.curdir))
    args = parser.parse_args()

    screenshot_dir = os.path.join(args.output_dir, "webview-snapshots")
    os.makedirs(screenshot_dir, exist_ok=True)
    try:
        login(args.username, args.password, args.device_code, screenshot_dir)
    finally:
        write_video(screenshot_dir, os.path.join(args.output_dir, "webview_recording.webm"))


def login(username: str, password: str, device_code: str, screenshot_dir: str = "."):
    Gtk.init(None)
    browser = BrowserWindow()
    browser.show_all()

    browser.web_view.load_uri("https://microsoft.com/devicelogin")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "page-loaded")

    browser.wait_for_text_visible("Enter code to allow access")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "device-login")
    browser.send_key_taps(
        ascii_string_to_key_events(device_code) + [Gdk.KEY_Return])

    browser.wait_for_text_visible("Sign in")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "device-login-enter-code")
    browser.send_key_taps(
        ascii_string_to_key_events(username) + [Gdk.KEY_Return])

    browser.wait_for_text_visible("Enter password")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "device-login-enter-username")
    browser.send_key_taps(
        ascii_string_to_key_events(password) + [Gdk.KEY_Return])

    browser.wait_for_text_visible("Are you trying to sign in")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "device-login-enter-password")
    browser.send_key_taps([Gdk.KEY_Return])

    browser.wait_for_text_visible("You have signed in")
    browser.wait_for_stable_page()
    capture_window_snapshot(browser, screenshot_dir, "device-login-success")


def capture_window_snapshot(window: Gtk.Window, path: str, filename: str = "snapshot"):
    global SNAPSHOT_INDEX

    # Get widget allocation (size)
    alloc = window.web_view.get_allocation()

    # Create an offscreen surface
    surface = cairo.ImageSurface(cairo.FORMAT_ARGB32, alloc.width, alloc.height)
    ctx = cairo.Context(surface)

    # Render the window contents onto the Cairo surface
    window.web_view.draw(ctx)

    # Write to file (PNG)
    file_path = os.path.join(path, f"{SNAPSHOT_INDEX:03}-{filename}.png")
    surface.write_to_png(file_path)
    SNAPSHOT_INDEX += 1

    return file_path


def write_video(screenshot_dir: str, video_path: str):
    subprocess.check_call([
        "ffmpeg",
        "-y",
        "-framerate", "1",
        "-pattern_type", "glob",
        "-i", f"{screenshot_dir}/*.png",
        video_path,
    ])


if __name__ == "__main__":
    main()
