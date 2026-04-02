import ctypes
import subprocess
import signal
import os
import time

from robot.api.deco import library, keyword
from robot.api import logger
from robot.libraries.BuiltIn import BuiltIn

PR_SET_PDEATHSIG = 1
SIGTERM = 15

# Ensure child processes are terminated when the parent process dies
def set_death_signal():
    libc = ctypes.CDLL('libc.so.6')
    libc.prctl(PR_SET_PDEATHSIG, SIGTERM)

def find_unused_display() -> int:
    used = set()
    for name in os.listdir('/tmp/.X11-unix'):
        if name.startswith('X'):
            try:
                used.add(int(name[1:]))
            except ValueError:
                continue
    for num in range(99, 200):
        if num not in used:
            return num
    raise RuntimeError("No unused display found")

def wait_for_xvfb(display_num: int, timeout: float = 10.0):
    socket_path = f'/tmp/.X11-unix/X{display_num}'
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if os.path.exists(socket_path):
            return
        time.sleep(0.1)
    raise RuntimeError(f"Xvfb display :{display_num} did not become ready in time")

def stop_process(proc: subprocess.Popen, stop_signal: int = signal.SIGTERM):
    """Terminate a process, killing it if it doesn't stop in time, and log its stderr."""
    if not proc:
        return
    name = proc.args[0]
    try:
        proc.send_signal(stop_signal)
        _, stderr = proc.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        logger.warn(f"{name} did not terminate, killing it.")
        proc.kill()
        _, stderr = proc.communicate(timeout=5)
    msg = f"{name} exited with exit code {proc.returncode}."
    if stderr:
        msg += f" stderr:\n{stderr}"
    logger.info(msg)

@library
class VNCRecorder:

    def __init__(self):
        self._xvfb_proc = None
        self._unclutter_proc = None
        self._viewer_proc = None
        self._ffmpeg_proc = None

    @keyword
    def start_recording(self, host='localhost', port=5901, resolution='1280x800'):
        """Start recording the VNC session to a video file."""
        output_dir = str(BuiltIn().get_variable_value('${SUITE_OUTPUT_DIR}'))
        output_path = os.path.join(output_dir, 'VM_Recording.webm')

        display_num = find_unused_display()
        display = f':{display_num}'

        # Start a virtual X server for the VNC session
        cmd = ['Xvfb', display, '-screen', '0', f'{resolution}x24']
        logger.info(f"Starting Xvfb with command: {' '.join(cmd)}")
        self._xvfb_proc = subprocess.Popen(
            cmd,
            text=True,
            stderr=subprocess.PIPE,
            preexec_fn=set_death_signal,
        )
        wait_for_xvfb(display_num)

        # Run unclutter to hide the mouse cursor so it doesn't show up in the recording
        cmd = ['unclutter', '-display', display, '-root', '-idle', '0.1']
        logger.info(f"Starting unclutter with command: {' '.join(cmd)}")
        self._unclutter_proc = subprocess.Popen(
            cmd,
            text=True,
            stderr=subprocess.PIPE,
            preexec_fn=set_death_signal,
        )

        # Start a VNC viewer on the virtual X server
        env = os.environ.copy()
        env['DISPLAY'] = display
        cmd = ['xtightvncviewer', '-shared', '-viewonly', '-fullscreen',
               f'{host}:{port}']
        logger.info(f"Starting xtightvncviewer with command: {' '.join(cmd)}")
        self._viewer_proc = subprocess.Popen(
            cmd,
            env=env,
            text=True,
            stderr=subprocess.PIPE,
            preexec_fn=set_death_signal,
        )

        # Give the viewer a moment to start and connect to the VNC server
        # before starting ffmpeg to avoid recording a black screen
        time.sleep(0.1)

        # Record the VNC session to a video file
        cmd = ['ffmpeg',
               '-loglevel', 'warning',
               '-y',
               '-f', 'x11grab',
               '-r', '25',
               '-s', resolution,
               '-i', f'{display}.0',
               '-codec:v', 'libvpx-vp9',
               '-preset', 'fast',
               '-crf', '23',
               output_path]
        logger.info(f"Starting ffmpeg with command: {' '.join(cmd)}")
        self._ffmpeg_proc = subprocess.Popen(
            cmd,
            text=True,
            stdin=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            preexec_fn=set_death_signal,
        )

    @keyword
    def stop_recording(self):
        """Stop recording and clean up."""
        stop_process(self._ffmpeg_proc, signal.SIGINT)
        stop_process(self._viewer_proc)
        stop_process(self._unclutter_proc)
        stop_process(self._xvfb_proc)
