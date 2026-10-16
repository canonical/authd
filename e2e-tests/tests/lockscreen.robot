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
    [Documentation]    A remote user can unlock an existing desktop session with the
    ...    local password created during device authentication.
    ...
    ...    Unlocking uses a separate PAM stack inside an existing session. The cached
    ...    password must work because the user cannot complete the device-code flow
    ...    from the lock screen.
    ...
    ...    The test runs ``loginctl lock-session`` and waits for LockedHint before
    ...    entering the password.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Lock Screen
    Unlock Screen With Password    ${local_password}
    Log Out

