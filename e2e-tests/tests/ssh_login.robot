*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with SSH
    [Documentation]    Verify the SSH login paths for a remote user: the first login
    ...    with the device code flow and later logins with the cached local password.
    ...
    ...    A first SSH authentication creates the account, so the broker only handles
    ...    names matching ``ssh_allowed_suffixes_first_auth``. The setup allows the
    ...    test account.
    ...
    ...    Steps:
    ...      1. ``ssh <user>@localhost``, run the device code flow, set a local
    ...         password, and check the account is set up correctly
    ...      2. log out and log in again over SSH with the local password
    ...
    ...    SSH drives authd through keyboard-interactive PAM rather than the terminal
    ...    UI the CLI tests use, so it has its own prompts ("Choose your provider:",
    ...    "Choose action:", "Create a local password:") and needs separate coverage.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow through SSH
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window

    # Log in with remote user with local password through SSH
    Open Terminal
    Log In With Remote User Through SSH: Local Password    ${username}    ${local_password}
