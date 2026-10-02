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
${local_password}    qwer1234


*** Test Cases ***
Test device registration during device code flow
    [Documentation]    A machine with ``register_device`` enabled registers as a
    ...    device in Microsoft Entra ID during device-code login.
    ...
    ...    Registration adds the machine to Entra ID for admins to manage
    ...    in the Entra admin center. The login flow stays the same; the broker
    ...    saves the registration data with the user's cached token.

    # Enable device registration before the first remote login, so the device is
    # registered as part of that login.
    Enable Device Registration

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}

    Check That Device Was Registered    ${username}

    Log Out From Terminal Session
    Close Focused Window
