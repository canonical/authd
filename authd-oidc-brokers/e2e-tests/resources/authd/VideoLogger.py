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
        output_dir = BuiltIn().get_variable_value('${OUTPUT DIR}', '.')
        pattern = os.path.join(output_dir, '*.webm')
        videos = glob.glob(pattern)
        for path in videos:
            description = os.path.basename(path).removesuffix('.webm').replace('_', ' ')
            with open(path, 'rb') as f:
                data = f.read()
            b64 = base64.b64encode(data).decode('utf-8')
            html = f'<video controls style="max-width: 50%" src="data:video/webm;base64,{b64}" />'
            logger.error(f'{description}\n{html}', html=True, console=False)
