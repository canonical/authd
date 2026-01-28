*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    Restore Snapshot    %{BROKER}-edge-configured
    Journal.Start Receiving Journal

Test Teardown
    Journal.Stop Receiving Journal
    Journal.Log Journal
    Log Videos On Error


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${new_password}    passwd1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test changing local password of remote user
    [Documentation]    This test verifies that a remote user can change their local password and subsequently log in using the new password.

    # Log in with local user
    Log In

    # Log in with remote user with device authentication
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Change local password of remote user
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Change Password    ${local_password}    ${new_password}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${new_password}
