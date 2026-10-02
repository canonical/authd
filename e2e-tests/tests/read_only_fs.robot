*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with GDM
    [Documentation]    Verify that a registered remote user can log in with their local
    ...    password while the root filesystem is mounted read-only.
    ...
    ...    authd writes to disk during a normal login, for example to update the user
    ...    record or create the home directory. When the filesystem is remounted
    ...    read-only, which the kernel does after certain I/O errors, an already
    ...    registered user must still be able to log in and investigate rather than
    ...    being locked out of a system that is still running.
    ...
    ...    Steps:
    ...      1. register the user with the device code flow while the filesystem is
    ...         still writable
    ...      2. remount the root filesystem read-only with the SysRq "u" trigger and
    ...         poll ``findmnt`` until the remount is visible
    ...      3. log in again with the local password
    ...
    ...    Despite the test name, the login goes through the CLI (``machinectl login``
    ...    and ``su``), not through GDM.

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
