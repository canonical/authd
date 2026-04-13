*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test that login fails if usernames do not match
    [Documentation]    This test verifies that when attempting to log in with a remote user whose username does not match the requested username, the login fails, while local users can still access the system.

    # Log in with local user
    Log In

    # Fail to log in if usernames do not match
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   different_user
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Check That Authenticated User Does Not Match Requested User    different_user
