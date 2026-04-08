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


*** Test Cases ***
Test that changing owner prevents remote logins
    [Documentation]    This test verifies that when the broker owner is changed to a different user, the original remote user cannot log in, while local users can still access the system.

    # Log in with local user
    Log In

    # Change owner to another user
    Change Broker Configuration    owner    different-user

    # Log in with remote user with device authentication
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Check That Remote User Is Not Allowed To Log In
