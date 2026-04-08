*** Settings ***
Resource        resources/authd/utils.resource
Resource        resources/authd/authd.resource

Resource        resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test that disabling broker prevents remote logins
    [Documentation]    This test verifies that when the broker is disabled, remote users cannot log in, while local users can still access the system.

    # Disable broker
    Disable Broker And Purge Config

    # Check that local user can still log in
    Log In

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that remote user cannot log in
    Open Terminal In Sudo Mode
    Try Log In With Remote User    ${username}
    Check That User Is Redirected To Local Broker
