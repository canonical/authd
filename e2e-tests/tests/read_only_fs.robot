*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}


*** Test Cases ***
Test login with GDM
    [Documentation]    Test that a user can log in with a local password when
    ...    the filesystem is read-only.

    # Re-mount the filesystem read-only
    SSH.Execute    echo u > /proc/sysrq-trigger
    # Wait for the SysRq remount to take effect
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    findmnt -n -o OPTIONS / | grep -qw ro

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
