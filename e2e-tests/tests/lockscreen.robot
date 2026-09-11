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

