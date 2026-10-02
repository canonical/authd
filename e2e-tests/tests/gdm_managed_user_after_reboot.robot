*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup       utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown    utils.Test Teardown


*** Variables ***
${username}        %{E2E_USER}
${local_password}  qwer1234


*** Test Cases ***
Managed user is listed by GDM after reboot
    [Documentation]    After a reboot, GDM lists the existing authd-managed user
    ...    automatically.
    ...
    ...    GDM builds its user list early in startup. If authd is not ready, the
    ...    account is missing and the user must type the name under "Not listed".
    ...    That can make the account look as if it disappeared.
    ...
    ...    The test registers the user through GDM, reads the display name over SSH,
    ...    reboots, and checks that the name appears in the greeter.
    ...
    ...    Do not query NSS after reboot. Socket activation would start authd and hide
    ...    the startup-ordering failure, so the test reads the name before rebooting.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Log Out
    ${display_name} =    SSH.Execute    getent passwd ${username} | cut -d: -f5 | cut -d, -f1
    Should Not Be Empty    ${display_name}
    Run Keyword And Ignore Error    SSH.Execute    systemctl reboot
    Wait Until GDM Login Screen Ready
    Match Text    ${display_name}    30
