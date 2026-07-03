#!/bin/bash

set -euo pipefail

# Install packages required for provisioning the e2e-tests VM
sudo apt-get update && sudo apt-get -y install \
    bsdutils \
    cloud-image-utils \
    guestfish \
    libvirt-clients-qemu \
    libvirt-daemon-system \
    qemu-system-x86 \
    retry \
    socat \
    xvfb \
    wget
