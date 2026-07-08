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
Test second login succeeds with force_access_check_with_provider enabled
    [Documentation]    Verify that a registered user can log in with their local
    ...    password when force_access_check_with_provider is enabled and
    ...    the identity provider is reachable.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    Change Broker Configuration    force_access_check_with_provider    true

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}


Test second login fails with force_access_check_with_provider enabled offline
    [Documentation]    Verify that a registered user cannot log in when
    ...    force_access_check_with_provider is enabled and the identity
    ...    provider is unreachable.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    Change Broker Configuration    force_access_check_with_provider    true

    # Block outbound HTTPS to simulate the identity provider being unreachable.
    Block Network Access To Identity Provider

    Open Terminal
    Try Log In With Remote User    ${username}
    Check That Remote User Has No Available Authentication Modes
