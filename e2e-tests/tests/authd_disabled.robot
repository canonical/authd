*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test that disabling authd prevents remote logins
    [Documentation]    This test verifies that when authd is disabled, remote users cannot log in, while local users can still access the system.

    # Disable authd
    Disable Authd Socket And Service

    # Check that local user can still log in
    Log In

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that remote user cannot log in
    Open Terminal In Sudo Mode
    Try Log In With Remote User    ${username}
    Check That Log In Fails Because Authd Is Disabled
