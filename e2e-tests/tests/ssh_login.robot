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
    [Documentation]    Test login via SSH with device code flow and local password.

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


Test local user can log in through SSH
    [Documentation]    Verify that SSH login with the local user's password
    ...    does not invoke the authd broker.

    Log In
    Open Terminal
    Log In With Local User Through SSH: Password
    Log Out From SSH Session
    Close Focused Window


Test second SSH login works with device authentication
    [Documentation]    Verify that an existing remote user can authenticate
    ...    through SSH with the device code flow.

    Log In

    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    Log Out From SSH Session
    Close Focused Window

    Enable Device Registration

    Open Terminal
    Log In With Remote User Through SSH: QR Code Existing User    ${username}    ${local_password}
    Log Out From SSH Session
    Close Focused Window


Test SSH login works for local and authd users with SSH keys
    [Documentation]    Verify that public-key authentication bypasses the
    ...    password PAM stack for both local and authd users.

    Log In

    Open Terminal
    Prepare SSH Key For User    ubuntu    e2e-local-key
    Log In With SSH Key    ubuntu    e2e-local-key
    Log Out From SSH Session
    Close Focused Window

    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window

    Prepare SSH Key For User    ${username}    e2e-authd-key
    Open Terminal
    Log In With SSH Key    ${username}    e2e-authd-key
    Log Out From SSH Session
    Close Focused Window
