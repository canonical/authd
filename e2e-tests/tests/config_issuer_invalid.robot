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


*** Test Cases ***
Test that invalid broker issuer prevents remote logins
    [Documentation]    This test verifies that when the broker is configured with an invalid issuer, remote users cannot log in, while local users can still access the system.

    # Log in with local user
    Log In

    # Change broker configuration to an invalid issuer
    Change Broker Configuration    issuer    invalid

    # Try to log in with remote user when broker has invalid issuer
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Check That Remote User Has No Available Authentication Modes
