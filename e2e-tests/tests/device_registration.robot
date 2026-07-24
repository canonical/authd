*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure
Test Tags         requires:msentraid

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test device registration during device code flow
    [Documentation]    Verify that enabling 'register_device' causes the machine
    ...    to be registered as a device in Microsoft Entra ID during device
    ...    code flow.
    ...
    ...    With 'register_device = true' the device-auth login flow is
    ...    unchanged from the user's perspective; the broker additionally
    ...    registers the device and persists the resulting registration
    ...    data in the user's cached token.

    # Enable device registration before the first remote login, so the device is
    # registered as part of that login.
    Enable Device Registration

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}

    Check That Device Was Registered    ${username}

    Log Out From Terminal Session
    Close Focused Window
