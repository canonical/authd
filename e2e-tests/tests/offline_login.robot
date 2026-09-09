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
Test second CLI login works offline after boot
    [Documentation]    Verify that a cached local password works when the
    ...    identity provider is unavailable after the machine has booted.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    Block Network Access To Identity Provider

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window


Test second GDM login works when the machine boots offline
    [Documentation]    Verify that a cached local password works when the
    ...    identity provider is unavailable before the next boot. The direct
    ...    password path also verifies that no device-code prompt is offered.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Log Out

    Block Network Access To Identity Provider
    SSH.Execute    systemctl mask NetworkManager.service
    Run Keyword And Ignore Error    SSH.Execute    systemctl reboot

    Wait Until GDM Login Screen Ready
    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}
    Log Out
