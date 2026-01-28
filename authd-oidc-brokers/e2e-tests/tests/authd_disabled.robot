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


*** Test Cases ***
Test that disabling authd prevents remote logins
    [Documentation]    This test verifies that when authd is disabled, remote users cannot log in, while local users can still access the system.

    # Log in with local user
    Log In

    # Disable authd
    Disable Authd Socket And Service

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that remote user cannot log in
    Open Terminal In Sudo Mode
    Try Log In With Remote User    ${username}
    Check That Log In Fails Because Authd Is Disabled
    Cancel Operation
    Close Terminal In Sudo Mode
