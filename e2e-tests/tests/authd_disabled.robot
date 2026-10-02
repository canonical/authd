*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test that disabling authd prevents remote logins
    [Documentation]    Verify that disabling authd blocks remote logins without
    ...    locking local users out of the machine.
    ...
    ...    authd inserts itself into the PAM stack for every login, so a broken or
    ...    stopped daemon must fail closed for the users it manages and stay out of
    ...    the way for everyone else. An administrator who disables authd still needs
    ...    a way back into the system.
    ...
    ...    authd is socket-activated, so both ``authd.socket`` and ``authd.service``
    ...    are masked; stopping only the service would let the socket start it again on
    ...    the first login attempt.
    ...
    ...    Checks performed (in order):
    ...      1. the local user can still log in through GDM
    ...      2. the local user can still become root with sudo
    ...      3. a remote login through ``machinectl login`` fails because the PAM
    ...         module cannot reach unix:///run/authd.sock

    # Disable authd
    Disable Authd Socket And Service

    # Check that local user can still log in
    Log In

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that remote user cannot log in
    Open Terminal
    Try Log In With Remote User    ${username}
    Check That Log In Fails Because Authd Is Disabled
