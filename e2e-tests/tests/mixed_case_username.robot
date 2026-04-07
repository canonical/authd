*** Settings ***
Resource        resources/authd/utils.resource
Resource        resources/authd/authd.resource

Resource        resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with mixed case username
    [Documentation]    Test login with mixed case username via CLI with device authentication and local password.

    # Log in with local user
    Log In

    # Log in with remote user using mixed case username with device authentication
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user using mixed case username with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
