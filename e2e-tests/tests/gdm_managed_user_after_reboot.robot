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
    [Documentation]    Verify that GDM discovers an existing managed user during boot.
    ...
    ...                Do not query NSS after the reboot: that would activate authd and
    ...                hide the startup-ordering regression this test covers.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Log Out
    ${display_name} =    SSH.Execute    getent passwd ${username} | cut -d: -f5 | cut -d, -f1

    Run Keyword And Ignore Error    SSH.Execute    systemctl reboot
    Wait Until Keyword Succeeds    3 min    1 sec
    ...    SSH.Execute    systemctl is-system-running --wait

    Wait Until GDM Login Screen Ready
    Match Text    ${display_name}    30
