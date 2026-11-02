*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Test Cases ***
Test that invalid broker issuer prevents remote logins
    [Documentation]    An invalid broker issuer leaves the remote user without an
    ...    authentication mode.
    ...
    ...    The broker gets its device-code and token endpoints from the issuer's
    ...    OIDC metadata. A bogus issuer makes discovery fail. After the user picks
    ...    the provider, the broker reports a connection error and offers no flow.
    ...
    ...    The test logs in as a local user first, then changes the issuer to invalid.

    # Log in with local user
    Log In

    # Change broker configuration to an invalid issuer
    Change Broker Configuration    issuer    invalid

    # Try to log in with remote user when broker has invalid issuer
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Check That Remote User Has No Available Authentication Modes
