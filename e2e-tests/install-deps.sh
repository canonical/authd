#!/bin/bash

set -euo pipefail

# Install packages required for running the e2e tests
sudo apt-get update && sudo apt-get -y install \
    bsdutils \
    ffmpeg \
    gir1.2-webkit2-4.1 \
    libcairo2-dev \
    libgirepository-2.0-dev \
    libvirt-clients-qemu \
    libvirt-daemon-system \
    libxkbcommon-dev \
    qemu-kvm \
    python3-cairo \
    python3-gi \
    python3-tk \
    socat \
    systemd-journal-remote \
    xvfb
