import os
import re
import unittest
from unittest.mock import patch

from broker_vars import get_variables


class EntraDeviceCodeTest(unittest.TestCase):
    def setUp(self):
        with patch.dict(os.environ, BROKER="authd-msentraid"):
            self.pattern = get_variables()["DEVICE_URL_REGEX"]

    def test_label_noise_does_not_replace_the_code(self):
        text = (
            "URL:\nhttps://login.microsoft.com/device\n"
            "Code:B\nABCD12345\n[ Request new code ]\n"
            "Press escape key to go back to choose the provider"
        )
        match = re.search(self.pattern, text)
        self.assertIsNotNone(match)
        self.assertEqual(match.groups()[-1], "ABCD12345")

    def test_existing_label_and_bare_formats(self):
        for prefix in ("", "URL:\n"):
            for label in (
                "",
                "Code:",
                "Code:\n",
                "code ",
                "Code\n",
                "Code\uff1a ",
                "Login code: ",
                "Login code\n",
            ):
                with self.subTest(prefix=prefix, label=label):
                    text = (
                        f"{prefix}https://login.microsoft.com/device\n"
                        f"{label}ABCD12345\n"
                    )
                    match = re.search(self.pattern, text)
                    self.assertIsNotNone(match)
                    self.assertEqual(match.groups()[-1], "ABCD12345")

    def test_incomplete_frames_do_not_start_browser_login(self):
        for code in (
            "Code:",
            "Code:B",
            "Code:\n",
            "Code:\nSign in",
            "Code:ABCD1234",
            "Code:ABCD123456",
            "Code:B\n[ Request new code ]",
        ):
            with self.subTest(code=code):
                text = f"URL:\nhttps://login.microsoft.com/device\n{code}\n"
                self.assertIsNone(re.search(self.pattern, text))
