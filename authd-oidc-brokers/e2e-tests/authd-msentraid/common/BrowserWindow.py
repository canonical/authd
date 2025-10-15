#!/usr/bin/env python3

import os
import sys
from time import sleep

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")

from gi.repository import Gdk, Gtk, GLib, WebKit2 as WebKit  # type: ignore


class BrowserWindow(Gtk.Window):
    def __init__(self):
        super().__init__(
            window_position=Gtk.WindowPosition.CENTER,
            default_width=1024,
            default_height=768,
            border_width=0,
            title="Authd Tests Browser Window",
        )

        self.web_view = WebKit.WebView()
        self.web_view.get_settings().enableJavascript = True
        self.web_view.get_settings().set_javascript_can_open_windows_automatically(
            False
        )
        self.web_view.grab_focus()

        self.web_view.set_can_default(True)
        self.web_view.grab_default()

        self.web_view.set_state_flags(
            Gtk.StateFlags.ACTIVE | Gtk.StateFlags.FOCUSED, True
        )

        def on_event(_, event):
            if (
                event.type != Gdk.EventType.KEY_PRESS
                and event.type != Gdk.EventType.KEY_RELEASE
            ):
                return
            print(f"event: ({event}, {event.keyval})")
            return False

        self.web_view.add_events(
            Gdk.EventMask.ALL_EVENTS_MASK
            & ~(Gdk.EventMask.EXPOSURE_MASK | Gdk.EventMask.STRUCTURE_MASK)
        )
        self.web_view.connect("event", on_event)

        self.load_state = WebKit.LoadEvent.STARTED

        def on_load_changed(_, load_event):
            self.load_state = load_event

        self.web_view.connect("load-changed", on_load_changed)

        self._overlay = Gtk.Overlay()
        self._overlay.add(self.web_view)

        self.add(self._overlay)

    def wait_for_page_loaded(self):
        if self.load_state == WebKit.LoadEvent.FINISHED:
            return

        loop = GLib.MainLoop()

        def on_load_changed(_, load_event):
            if load_event != WebKit.LoadEvent.FINISHED:
                return

            loop.quit()

        signal_id = self.web_view.connect("load-changed", on_load_changed)
        loop.run()
        self.web_view.disconnect(signal_id)

    def wait_for_stable_page(self):
        self.wait_for_page_loaded()

        overlay = Gtk.Button()
        overlay.set_opacity(0)
        self._overlay.add_overlay(overlay)
        overlay.show()
        overlay.grab_focus()

        loop = GLib.MainLoop()

        def on_timeout():
            loop.quit()
            return False

        draw_timeout = 500
        timeout = GLib.timeout_add(draw_timeout, on_timeout)

        def on_draw_event(_, de):
            nonlocal timeout

            GLib.source_remove(timeout)
            timeout = GLib.timeout_add(draw_timeout, on_timeout)
            return False

        signal_id = self.web_view.connect("draw", on_draw_event)
        loop.run()
        self.web_view.disconnect(signal_id)

        overlay.destroy()
        self.web_view.grab_focus()

    def send_key(self, event_type, key):
        default_seat = Gdk.Display.get_default().get_default_seat()
        event = Gdk.Event.new(event_type)
        event.set_device(default_seat.get_keyboard())
        event.set_source_device(default_seat.get_keyboard())
        event.window = self.web_view.get_window()
        event.send_event = True
        event.keyval = key

        loop = GLib.MainLoop()

        def on_event(_, event):
            if event.type == event_type and event.keyval == key:
                loop.quit()
            return False

        self.web_view.connect("event", on_event)
        event.put()
        loop.run()

    def send_key_tap(self, key):
        self.send_key(Gdk.EventType.KEY_PRESS, key)
        self.send_key(Gdk.EventType.KEY_RELEASE, key)

    def send_key_taps(self, key_taps):
        for kt in key_taps:
            self.send_key_tap(kt)


def ascii_string_to_key_events(string):
    if len(string) != len(string.encode()):
        raise TypeError(f"{string} is not an ascii string")
    return [ord(ch) for ch in list(string)]


def timeout_handler(signum, frame):
    raise TimeoutError("Operation timed out")

if __name__ == "__main__":
    if os.getenv("RUN_OFFSCREEN") == "1" and "RUNNING_OFFSCREEN" not in os.environ:
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

    sys.exit(0)
