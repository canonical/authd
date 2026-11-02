*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown       utils.Test Teardown


*** Variables ***
${snapshot}             %{BROKER}-installed
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Test Cases ***
Test login with local password on read-only filesystem
    [Documentation]    An already registered remote user can log in with a local
    ...    password while the root filesystem is read-only.
    ...
    ...    A normal login may update the user record or create the home directory.
    ...    After some I/O errors, the kernel can remount the filesystem read-only.
    ...    An existing user should still be able to log in and investigate.
    ...
    ...    The test registers the user while the filesystem is writable, then
    ...    remounts root read-only with SysRq ``u``. It waits for ``findmnt`` to show
    ...    the change, logs in with the local password, and uses ``machinectl login``
    ...    and ``su`` rather than GDM.
    # Log in with local user
    Log In

    # Log in with remote user with the device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Re-mount the filesystem read-only
    SSH.Execute    echo u > /proc/sysrq-trigger
    # Wait for the SysRq remount to take effect
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    findmnt -n -o OPTIONS / | grep -qw ro

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
