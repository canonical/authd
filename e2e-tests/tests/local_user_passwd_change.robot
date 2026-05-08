*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${new_password}    Passwd1234


*** Test Cases ***
Test changing local password of local user
    [Documentation]    This test verifies that a local user can still change their local password with authd installed.

    # Log in with local user
    Log In

    # Change password for local user
    Open Terminal In Sudo Mode
    Force Password Change
    Close Terminal In Sudo Mode
    Log Out

    # Log in with new password
    Log In And Set Password    ${new_password}
