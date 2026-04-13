from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore
from robot.libraries.BuiltIn import BuiltIn
import glob
import os
import base64

@library
class VideoLogger:
    """Robot library exposing the `Log Videos` keyword."""

    @keyword
    def log_videos(self):
        output_dir = str(BuiltIn().get_variable_value('${SUITE_OUTPUT_DIR}'))
        pattern = os.path.join(output_dir, '*.mp4')
        videos = sorted(glob.glob(pattern))
        for path in videos:
            title = os.path.basename(path).removesuffix('.mp4').replace('_', ' ')
            relpath = os.path.relpath(path, os.path.dirname(output_dir))
            # preload="metadata" fetches only the video duration and first frame without
            # downloading the full video, keeping the HTML log page fast to load.
            html = f'<video controls style="max-width: 50%;" preload="metadata"><source src="{relpath}" type="video/mp4"></video>'
            BuiltIn().set_test_message(f'*HTML*<h3 data-skip-stderr>{title}</h3>{html}', append=True, separator='\n')
