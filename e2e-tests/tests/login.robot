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
Test login with CLI
    [Documentation]    Verify the core CLI login paths for a remote user: the first
    ...    login through ``machinectl login`` with the device code flow, and later
    ...    logins through ``su`` with the cached local password.
    ...
    ...    Steps:
    ...      1. register the user with the device code flow, set a local password, and
    ...         check the user is visible through NSS, has a home directory owned by
    ...         them, and, for MS Entra ID, is in the expected groups and can run sudo
    ...      2. log in again with ``su`` using only the local password
    ...      3. check the user cannot get back to user or provider selection from an
    ...         ``su`` login. For a known user the provider is already recorded, so
    ...         Escape stops at the authentication-flow screen and cancelling there
    ...         aborts ``su`` instead of letting the caller pick another identity
    ...      4. check that ``su`` to a local user is still handled by the local broker
    ...         rather than by authd

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Check Home Directory    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    # Try to change username during su login, it should not be possible
    Open Terminal
    Check That Username Cannot Be Changed When Using su    ${username}
    Clear Terminal

    # Check that `su` to a local user goes to the local broker, not authd.
    Check That su To Local User Goes To Local Broker
