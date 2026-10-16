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
    [Documentation]    A registered user can log in with a local password while the
    ...    provider is reachable, even with ``force_access_check_with_provider`` set.
    ...
    ...    Local-password logins already check the provider when it is reachable. If
    ...    it is not, the broker can use a cached token unless this option is enabled.
    ...    The option disables that offline fallback; it does not add an online
    ...    revocation check.
    ...
    ...    The test registers the user through device code before enabling the option,
    ...    while the provider is reachable.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    Change Broker Configuration    force_access_check_with_provider    true

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}


Test second login fails with force_access_check_with_provider enabled offline
    [Documentation]    An unreachable provider blocks login when
    ...    ``force_access_check_with_provider`` is enabled.
    ...
    ...    The test blocks outbound HTTPS. Session creation then fails while the
    ...    broker connects to the provider, before it offers an authentication mode.
    ...    The test checks this connection error; it does not reach token refresh.
    ...
    ...    The test leaves the iptables rules in place. The next test restores the
    ...    VM snapshot.
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
