from robot.api.deco import keyword, library  # type: ignore
from robot.libraries.BuiltIn import BuiltIn
import glob
import os

from RecordingUtils import current_test_recording_suffix

@library
class VideoLogger:
    """Robot library exposing the `Log Videos` keyword."""

    @keyword
    def log_videos(self):
        output_dir = str(BuiltIn().get_variable_value('${SUITE_OUTPUT_DIR}'))
        test_video_suffix = current_test_recording_suffix()
        patterns = (
            os.path.join(output_dir, f'VM_Recording_{test_video_suffix}'),
            os.path.join(output_dir, f'Webview_Recording_{test_video_suffix}'),
        )
        videos = sorted(path for pattern in patterns for path in glob.glob(pattern))
        for path in videos:
            title = os.path.basename(path).removesuffix('.mp4').replace('_', ' ')
            relpath = os.path.relpath(path, os.path.dirname(output_dir))
            # preload="metadata" fetches only the video duration and first frame without
            # downloading the full video, keeping the HTML log page fast to load.
            html = f'<video controls style="max-width: 50%;" preload="metadata"><source src="{relpath}" type="video/mp4"></video>'
            BuiltIn().set_test_message(f'*HTML*<h3 data-skip-stderr>{title}</h3>{html}', append=True, separator='\n')
