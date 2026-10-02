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
    [Documentation]    Verify that GDM discovers an existing authd-managed user while
    ...    the machine boots.
    ...
    ...    GDM builds its user list early in the boot sequence. If authd is not ready
    ...    by then, managed users are missing from the greeter and have to be typed in
    ...    under "Not listed", which looks like the account disappeared.
    ...
    ...    The user is registered through GDM with the device code flow, their display
    ...    name is read over SSH, and the machine is rebooted. The display name must
    ...    then show up on the greeter on its own.
    ...
    ...    Do not query NSS after the reboot: that would activate authd through socket
    ...    activation and hide the startup-ordering regression this test covers. The
    ...    display name is therefore read before rebooting.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Log Out
    ${display_name} =    SSH.Execute    getent passwd ${username} | cut -d: -f5 | cut -d, -f1
    Should Not Be Empty    ${display_name}
    Run Keyword And Ignore Error    SSH.Execute    systemctl reboot
    Wait Until GDM Login Screen Ready
    Match Text    ${display_name}    30
