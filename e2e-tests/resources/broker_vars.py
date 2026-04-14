"""Broker-specific variable definitions for the e2e tests.

Loaded as a Robot Framework variable file by broker.resource.
Reads the BROKER environment variable and returns the corresponding
variable values, so no broker-specific resource files are needed.
"""

import os

_BROKER_CONFIGS = {
    "authd-msentraid": {
        "BROKER_SNAP_NAME": "authd-msentraid",
        "AUTHD_BROKER_CFG": "/etc/authd/brokers.d/msentraid.conf",
        "BROKER_CFG": "/var/snap/authd-msentraid/current/broker.conf",
        "BROKER_CFG_DIR": "/var/snap/authd-msentraid/current/broker.conf.d",
        "PROVIDER_DISPLAY_NAME": "Microsoft Entra ID",
        "DEVICE_URL": "login.microsoft.com/device",
        # TODO: Once the stable channel ships the new URL:/Code: format, drop the
        # optional-prefix fallbacks and simplify to r"URL:\s*(https://)?...\nCode:\s*([A-Za-z0-9]+)".
        # They exist only for migration_broker.robot, which logs in before upgrading
        # from stable (old bare format) to edge (new labeled format).
        "DEVICE_URL_REGEX": r"(?:URL:\s*)?(https://)?login.microsoft.com/device\n(?:(?:Code|Login code):\s*|\s*)([A-Za-z0-9]+)",
        "remote_group": "e2e-test-group",
    },
    "authd-google": {
        "BROKER_SNAP_NAME": "authd-google",
        "AUTHD_BROKER_CFG": "/etc/authd/brokers.d/google.conf",
        "BROKER_CFG": "/var/snap/authd-google/current/broker.conf",
        "BROKER_CFG_DIR": "/var/snap/authd-google/current/broker.conf.d",
        "PROVIDER_DISPLAY_NAME": "Google",
        "DEVICE_URL": "google.com/device",
        # TODO: Same as above — simplify once stable ships the new format.
        "DEVICE_URL_REGEX": r"(?:URL:\s*)?(https:\/\/)?google.com\/device\n(?:(?:Code|Login code):\s*|\s*)([A-Za-z\- ]+)",
        "remote_group": "",
    },
}


def get_variables():
    broker = os.environ.get("BROKER", "")
    config = _BROKER_CONFIGS.get(broker)
    if config is None:
        known = ", ".join(_BROKER_CONFIGS)
        raise ValueError(
            f"Unknown or missing BROKER environment variable: {broker!r}. "
            f"Known brokers: {known}"
        )
    return config
