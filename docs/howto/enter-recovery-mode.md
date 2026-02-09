---
myst:
  html_meta:
    "description lang=en": "Guide on entering recovery mode after a failed login attempt with authd."
---

# How to enter recovery mode after failed login

If authd and/or the broker are missing, corrupted, or broken in any way, a user may
be prevented from logging in.

To get access to the system for modifying configurations and installations in
such cases, there are two main options:

1. Log in as root user or another local user with administrator privileges
2. Boot into recovery mode to get root access

The steps required for entering recovery mode are included below.

## Boot into recovery mode

If it is not possible to log in with the root user account or another local
user account with administrator privileges, the user can boot into recovery
mode:

1. Reboot the device
2. During the reboot, press and hold the right <kbd>SHIFT</kbd> key
3. When the Grub menu appears, select `advanced options for Ubuntu`
4. Choose `recovery mode` for the correct kernel version
5. Select `drop to root shell prompt`

The user then has access to the root filesystem and can proceed with debugging.
