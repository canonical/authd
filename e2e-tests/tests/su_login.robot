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
Test su login
    [Documentation]    Test login functionality with su command for remote users.

    # Log in with local user
    Log In

    # Log in with remote user with device authentication
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}

    # Try to change username during su login, it should not be possible
    Try Changing Username In su Log In    ${username}

    # Try to change username during su login with sudo, it should not be possible
    Try Changing Username In sudo su Log In
