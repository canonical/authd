#!/usr/bin/env bash

set -euo pipefail

echo "Running ${0##*/}"

systemctl mask unattended-upgrades.service
rm -f /etc/apt/apt.conf.d/20auto-upgrades /etc/apt/apt.conf.d/50unattended-upgrades
