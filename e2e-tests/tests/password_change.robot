*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${new_password}    passwd1234


*** Test Cases ***
Test changing local password of remote user
    [Documentation]    This test verifies that a remote user can change their
    ...    local password and subsequently log in using the new password.

    # Change local password of remote user
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Change Password    ${local_password}    ${new_password}
    Log Out From su Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${new_password}
