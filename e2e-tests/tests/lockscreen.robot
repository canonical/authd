*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

Test Setup    checkpoints.authd User Logged In Via GDM
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test remote user can unlock the lock screen with a local password
    [Documentation]    Verify that a remote user can unlock a locked desktop
    ...    session with the local password created during device authentication.

    # The GDM checkpoint leaves the remote user's session active.
    Lock Screen
    Unlock Screen With Password    ${local_password}
    Log Out
