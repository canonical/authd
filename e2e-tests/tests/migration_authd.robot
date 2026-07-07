*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-stable-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login after updating authd to edge version
    [Documentation]    Test login via CLI with device code flow and local password after switching to the edge PPA for authd.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    # Switch to the edge PPA for authd
    Enable Edge Repository For Authd
    Update Authd

    # Log in with remote user with local password after upgrading
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
