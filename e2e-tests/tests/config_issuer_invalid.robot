*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test that invalid broker issuer prevents remote logins
    [Documentation]    Verify that a broker configured with an invalid issuer offers no
    ...    authentication mode to a remote user.
    ...
    ...    The broker discovers the device code and token endpoints from the issuer's
    ...    OIDC metadata. With a bogus issuer that discovery fails, so after the user
    ...    picks the provider the broker has no flow left to offer and reports that it
    ...    cannot connect to the provider.
    ...
    ...    The local user logs in first to confirm that a broken broker configuration
    ...    does not affect local authentication.

    # Log in with local user
    Log In

    # Change broker configuration to an invalid issuer
    Change Broker Configuration    issuer    invalid

    # Try to log in with remote user when broker has invalid issuer
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Check That Remote User Has No Available Authentication Modes
