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
    [Documentation]    Disabling authd blocks remote logins while local accounts
    ...    keep access to the machine.
    ...
    ...    PAM calls authd for every login. When the daemon is down, managed users
    ...    must be denied, while local users and administrators still need a way in.
    ...
    ...    authd uses socket activation, so the test masks both
    ...    ``authd.socket`` and ``authd.service``. Stopping only the service would
    ...    let the socket restart it on the next login.
    ...
    ...    The test checks these cases in order:
    ...    - the local user can still log in through GDM
    ...    - the local user can still become root with sudo
    ...    - a remote ``machinectl login`` fails because the PAM module cannot
    ...      reach unix:///run/authd.sock

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
