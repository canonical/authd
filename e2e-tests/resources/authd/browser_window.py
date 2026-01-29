import cairo
import json
import gi
import os
import tempfile
import traceback
import sys

import ExecUtils

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")

from gi.repository import (
    Gdk,
    Gtk,
    GLib,
    Gio,
    WebKit2 as WebKit
)  # type: ignore


class BrowserWindow(Gtk.Window):
    def __init__(self):
        super().__init__(
            window_position=Gtk.WindowPosition.CENTER,
            default_width=1024,
            default_height=768,
            border_width=0,
            title="Authd Tests Browser Window",
        )

        self._draw_monitors_cancellable = None
        self._draw_monitors = []
        self._snapshots_indexes = {}
        self._snapshotting = False
        self._recording_path = None
        self._recording_fps = 0
        self._recording_cancellable = None
        self._recoding_cancellable_id = 0

        self.web_view = WebKit.WebView()
        self.web_view.get_settings().enableJavascript = True
        self.web_view.get_settings().set_javascript_can_open_windows_automatically(
            False
        )
        self.web_view.get_settings().set_user_agent(
            "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:143.0) Gecko/20100101 Firefox/143.0"
        )

        self.web_view.set_can_default(True)

        self.web_view.set_state_flags(
            Gtk.StateFlags.ACTIVE | Gtk.StateFlags.FOCUSED, True
        )

        def on_event(_, event):
            if (
                event.type != Gdk.EventType.KEY_PRESS
                and event.type != Gdk.EventType.KEY_RELEASE
            ):
                return
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

        self.web_view.grab_default()
        self.web_view.grab_focus()

        self.connect("destroy", lambda _wv: self._on_destroy())

    def _on_destroy(self):
        if self._draw_monitors_cancellable:
            self._draw_monitors_cancellable.cancel()

        self.stop_recording()

    def draw_event_connect(self, callback):
        idle_id = 0

        def on_idle():
            nonlocal idle_id

            for cb in self._draw_monitors:
                cb()
            idle_id = 0
            return False

        def on_draw_event(_, _cr):
            nonlocal idle_id

            if self._snapshotting:
                return False

            if not idle_id:
                idle_id = GLib.idle_add(on_idle)
            return False

        self._draw_monitors.append(callback)
        signal_id = self.web_view.connect_after("draw", on_draw_event)

        def on_cancelled():
            self._draw_monitors_cancellable = None

            if idle_id:
                GLib.source_remove(idle_id)

            self.web_view.disconnect(signal_id)

        if not self._draw_monitors_cancellable:
            self._draw_monitors_cancellable = Gio.Cancellable()
            self._draw_monitors_cancellable.connect(on_cancelled)

    def draw_event_disconnect(self, callback):
        self._draw_monitors.remove(callback)

        if not self._draw_monitors and self._draw_monitors_cancellable:
            self._draw_monitors_cancellable.cancel()

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

        # This overlay serves us to ensure that focus-related elements of the
        # page (such as the cursor blinking) aren't affecting our page changes
        # check mechanism.
        # So we add an overlay and temporarily steal the focus.
        overlay = Gtk.Button()
        overlay.set_opacity(0)
        self._overlay.add_overlay(overlay)
        overlay.show()
        overlay.grab_focus()

        loop = GLib.MainLoop()

        def on_timeout():
            loop.quit()
            return False

        draw_timeout = 750
        timeout = GLib.timeout_add(draw_timeout, on_timeout)

        def on_draw_event():
            nonlocal timeout

            GLib.source_remove(timeout)
            timeout = GLib.timeout_add(draw_timeout, on_timeout)

        self.draw_event_connect(on_draw_event)
        loop.run()
        self.draw_event_disconnect(on_draw_event)

        overlay.destroy()
        self.web_view.grab_focus()

    def wait_for_pattern(self, pattern, timeout_ms=5000,
                         poll_interval_ms=100) -> str|None:
        """Wait until `text` is present in the page's visible text and return the matched substring."""
        loop = GLib.MainLoop()
        cancellable = Gio.Cancellable()
        inject_delay_id = 0
        timeout_id = 0
        found = None

        # Use json.dumps / JSON.parse to safely escape the text into a JS string literal
        # Return the first match (or empty string if none)
        js = """(function(){
                        try {
                            var pattern = JSON.parse(`%s`);
                            var re = new RegExp(pattern);
                            var body = document && document.body && document.body.innerText ? document.body.innerText : '';
                            var m = body.match(re);
                            return m ? m[0] : '';
                        } catch (e) {
                            return '';
                        }
                    })()""" % json.dumps(pattern)

        def on_js_finished(web_view, result):
            nonlocal inject_delay_id, found

            final_action = cancellable.cancel
            try:
                res = web_view.run_javascript_finish(result)
                js_value = res.get_js_value()
                match_str = js_value.to_string()
                if not match_str:
                    # Retry
                    final_action = lambda: None
                    inject_javascript()
                    return

                found = match_str
            except GLib.Error as e:
                if e.matches(Gio.io_error_quark(), Gio.IOErrorEnum.CANCELLED):
                    return
                raise e
            except Exception as e:
                raise e
            finally:
                final_action()

        def on_inject_js_timeout():
            nonlocal inject_delay_id

            self.web_view.run_javascript(js, cancellable, on_js_finished)
            inject_delay_id = 0
            return False

        def inject_javascript():
            nonlocal inject_delay_id

            inject_delay_id = GLib.timeout_add(poll_interval_ms, on_inject_js_timeout)

        def on_cancelled():
            loop.quit()

            if timeout_id:
                GLib.source_remove(timeout_id)

            if inject_delay_id:
                GLib.source_remove(inject_delay_id)

        connect_id = cancellable.connect(on_cancelled)
        timeout_id = GLib.timeout_add(timeout_ms, cancellable.cancel)

        inject_javascript()
        loop.run()

        cancellable.disconnect(connect_id)

        if not found:
            raise TimeoutError(f"Timed out waiting for text: \"{pattern}\"")

        return found

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

    def _run_async_task(self, task_function, cancellable: Gio.Cancellable = None,
                        wait: bool = True):
        loop = None
        ret = False

        def callback(_obj, result):
            nonlocal ret

            try:
                ret = result.propagate_boolean()
            except GLib.Error as e:
                if e.matches(Gio.io_error_quark(), Gio.IOErrorEnum.CANCELLED):
                    return
            except Exception as e:
                raise e
            finally:
                if loop:
                    loop.quit()

        def thread_func(t, _so, _td, _c):
            try:
                task_function()
                t.return_boolean(True)
            except GLib.Error as e:
                t.return_error(e)
            except Exception as e:
                print(traceback.format_exc(), file=sys.stderr)
                t.return_error(GLib.Error(f"{e}"))

        if wait:
            loop = GLib.MainLoop()

        task = Gio.Task.new(source_object=self, cancellable=cancellable,
                            callback=callback)
        task.run_in_thread(thread_func)

        if wait:
            loop.run()
            return ret

        return True

    def capture_snapshot(self, path: str, filename: str = "snapshot", ext: str = "png",
                         sync: bool = True, cancellable: Gio.Cancellable = None):
        view_window = self.web_view.get_window()
        scale = view_window.get_scale_factor()
        width = view_window.get_width() * scale
        height = view_window.get_height() * scale

        # Create an offscreen surface
        try:
            # This is failing in older PyGObject versions, so let's try both ways.
            surface = view_window.create_similar_image_surface(cairo.Format.ARGB32,
                                                               width, height, scale)
        except ValueError:
            surface = cairo.ImageSurface(cairo.Format.ARGB32, width, height)
            surface.set_device_scale(scale, scale)

        ctx = cairo.Context(surface)

        # Render the window contents onto the Cairo surface, blocking any
        # draw event handler to prevent reentrance
        self._snapshotting = True
        self.web_view.draw(ctx)
        self._snapshotting = False

        # Write to file
        snapshot_index = self._snapshots_indexes.setdefault(path, 0)
        file_path = os.path.join(path, f"{snapshot_index:05}-{filename}.{ext}")
        self._snapshots_indexes[path] += 1

        self._run_async_task(lambda: surface.write_to_png(file_path),
                             cancellable=cancellable, wait=sync)
        return file_path

    def start_recording(self, fps: int = 5):
        if self._recording_cancellable:
            raise Exception("Recording is already in progress")

        cancellable = Gio.Cancellable()
        timeout = 0
        max_delay_ms = 1000 / fps

        self._recording_path = tempfile.TemporaryDirectory(prefix="authd-browser")

        def save_snapshot():
            nonlocal timeout

            self.capture_snapshot(self._recording_path.name,
                                  filename="frame", sync=False,
                                  cancellable=cancellable)

            timeout = GLib.timeout_add(max_delay_ms, save_snapshot)

        save_snapshot()

        def on_cancelled():
            if timeout:
                GLib.source_remove(timeout)

            self._recording_cancellable = None

        self._recoding_cancellable_id = cancellable.connect(on_cancelled)
        self._recording_cancellable = cancellable
        self._recording_fps = fps

    def stop_recording(self, rendered_output: str = None):
        if not self._recording_cancellable:
            return

        cancellable = self._recording_cancellable
        self._recording_cancellable.cancel()
        cancellable.disconnect(self._recoding_cancellable_id)
        self._recoding_cancellable_id = 0

        if rendered_output:
            self._run_async_task(lambda: render_video(self._recording_path.name,
                                                      rendered_output,
                                                      self._recording_fps))

        self._recording_path.cleanup()
        self._recording_fps = 0


def ascii_string_to_key_events(string):
    if len(string) != len(string.encode()):
        raise TypeError(f"{string} is not an ascii string")
    return [ord(ch) for ch in list(string)]


def render_video(screenshot_dir: str, video_path: str, framerate: int = 1):
    ExecUtils.check_call([
        "ffmpeg",
        "-loglevel", "warning",
        "-y",
        "-framerate", str(framerate),
        "-pattern_type", "glob",
        "-i", f"{screenshot_dir}/*.png",
        video_path,
    ])
