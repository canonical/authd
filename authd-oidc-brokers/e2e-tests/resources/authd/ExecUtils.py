from robot.api import logger

import subprocess


def check_call(*args, **kwargs):
    try:
        return subprocess.check_call(*args, **kwargs)
    except subprocess.CalledProcessError as e:
        if e.stderr:
            logger.error(e.stderr)
        raise e


def check_output(*args, **kwargs):
    try:
        return subprocess.check_output(*args, **kwargs)
    except subprocess.CalledProcessError as e:
        if e.stderr:
            logger.error(e.stderr)
        raise e


def run(*args, **kwargs):
    try:
        return subprocess.run(*args, **kwargs)
    except subprocess.CalledProcessError as e:
        if e.stderr:
            logger.error(e.stderr)
        raise e


class Popen(subprocess.Popen):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

    def communicate(self, *args, **kwargs):
        try:
            return super().communicate(*args, **kwargs)
        except subprocess.CalledProcessError as e:
            if e.stderr:
                logger.error(e.stderr)
            raise e
