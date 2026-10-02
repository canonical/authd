*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with mixed case username
    [Documentation]    Verify that a remote user whose name contains upper-case
    ...    characters can log in through the CLI, first with the device code flow and
    ...    then with the cached local password.
    ...
    ...    authd lower-cases user and group names before storing them, because POSIX
    ...    user names are case-sensitive while identity providers hand out names in
    ...    their original casing. PAM still receives the name exactly as typed, so
    ...    both logins have to normalise it the same way and resolve to one account.
    ...
    ...    The name comes from ``E2E_USER``, so this only exercises the mixed-case path
    ...    when that account is configured with the provider's original casing.

    # Log in with local user
    Log In

    # Log in with remote user using mixed case username with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user using mixed case username with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
