*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

Test Setup       checkpoints.authd User Logged In Via GDM
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
    # The GDM checkpoint already logged in as the user; log out before rebooting.
    Log Out
    ${display_name} =    SSH.Execute    getent passwd ${username} | cut -d: -f5 | cut -d, -f1
    Should Not Be Empty    ${display_name}
    Run Keyword And Ignore Error    SSH.Execute    systemctl reboot
    Wait Until GDM Login Screen Ready
    Match Text    ${display_name}    30
