#!/usr/bin/env bash

set -euo pipefail

echo "Running ${0##*/}"

systemctl mask apt-daily.service
systemctl mask apt-daily.timer
systemctl mask apt-daily-upgrade.service
systemctl mask apt-daily-upgrade.timer
