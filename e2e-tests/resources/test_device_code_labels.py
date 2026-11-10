import os
import re
import unittest
from unittest.mock import patch

from broker_vars import get_variables


class DeviceCodeLabelTest(unittest.TestCase):
    def test_labeled_formats_preserve_ocr_tolerance(self):
        for broker, url, code in (
            ("authd-msentraid", "login.microsoft.com/device", "ABCD12345"),
            ("authd-google", "google.com/device", "ABC-DEF"),
            ("authd-google", "google.com/device", "ABCD-EFGH"),
            ("authd-google", "google.com/device", "a b c d - e f g h"),
        ):
            with patch.dict(os.environ, BROKER=broker):
                pattern = get_variables()["DEVICE_URL_REGEX"]
            for prefix in ("URL: ", "URL:\n"):
                for scheme in ("", "https://"):
                    for label in ("Code:", "Code:\n", "code ", "Code\n", "Code\uff1a "):
                        with self.subTest(
                            broker=broker,
                            prefix=prefix,
                            scheme=scheme,
                            label=label,
                            code=code,
                        ):
                            text = f"{prefix}{scheme}{url}\n{label}{code}\n"
                            match = re.search(pattern, text)
                            self.assertIsNotNone(match)
                            self.assertEqual(match.groups()[-1], code)

    def test_legacy_formats_are_not_accepted(self):
        for broker, url, code in (
            ("authd-msentraid", "login.microsoft.com/device", "ABCD12345"),
            ("authd-google", "google.com/device", "ABCD-EFGH"),
        ):
            with patch.dict(os.environ, BROKER=broker):
                pattern = get_variables()["DEVICE_URL_REGEX"]
            for prefix, label in (
                ("", ""),
                ("", "Code:"),
                ("URL: ", ""),
                ("URL: ", "Login code: "),
            ):
                with self.subTest(broker=broker, prefix=prefix, label=label):
                    text = f"{prefix}https://{url}\n{label}{code}\n"
                    self.assertIsNone(re.search(pattern, text))
