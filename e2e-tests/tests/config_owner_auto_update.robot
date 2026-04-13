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
Test that owner is auto-updated in broker configuration
    [Documentation]    This test verifies that when a local user logs in, the broker configuration is automatically updated to set the owner to the logged-in user.

    # Log in with local user
    Log In

    # Try to log in with remote user
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Check that owner was updated in broker configuration
    Open Terminal In Sudo Mode
    Check If Owner Was Registered    ${username}
