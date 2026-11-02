*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          Test Setup
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Test Cases ***
Test login with SSH
    [Documentation]    The first SSH login uses device code. Later logins use the
    ...    cached local password.
    ...
    ...    On first login, the broker handles names matching
    ...    ``ssh_allowed_suffixes_first_auth``. The test allows the E2E account.
    ...
    ...    The first login uses ``ssh <user>@localhost`` for device-code flow, sets
    ...    a local password, and checks account setup. A later SSH login uses that
    ...    password.
    ...
    ...    SSH uses keyboard-interactive PAM and prompts with "Choose your provider:",
    ...    "Choose action:", and "Create a local password:". This differs from the
    ...    CLI terminal UI, so SSH needs separate coverage.

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


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}
