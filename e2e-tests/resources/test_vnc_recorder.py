import unittest
from unittest.mock import patch

from VNCRecorder import find_unused_display


class FindUnusedDisplayTest(unittest.TestCase):
    def test_missing_socket_directory(self):
        with patch("VNCRecorder.os.listdir", side_effect=FileNotFoundError):
            self.assertEqual(find_unused_display(), 99)

    def test_skips_existing_displays(self):
        with patch(
            "VNCRecorder.os.listdir",
            return_value=["X99", "X100", "X102", "Xinvalid", "other"],
        ):
            self.assertEqual(find_unused_display(), 101)

    def test_does_not_hide_permission_errors(self):
        with patch("VNCRecorder.os.listdir", side_effect=PermissionError):
            with self.assertRaises(PermissionError):
                find_unused_display()
