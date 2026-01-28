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
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test that login fails if usernames do not match
    [Documentation]    This test verifies that when attempting to log in with a remote user whose username does not match the requested username, the login fails, while local users can still access the system.

    # Log in with local user
    Log In

    # Fail to log in if usernames do not match
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   different_user
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser   ${username}
    Check That Authenticated User Does Not Match Requested User    different_user
