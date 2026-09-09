*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test remote user can unlock the lock screen with a local password
    [Documentation]    Verify that a remote user can unlock a locked desktop
    ...    session with the local password created during device authentication.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Lock Screen
    Unlock Screen With Password    ${local_password}
    Log Out


Test remote user can unlock the lock screen with device authentication
    [Documentation]    Verify that a remote user can unlock a locked desktop
    ...    session through the device code flow. Enabling device registration
    ...    makes the device-code mode the only available mode for this user.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Enable Device Registration
    Lock Screen
    Unlock Screen With Device Code    ${local_password}
    Log Out
