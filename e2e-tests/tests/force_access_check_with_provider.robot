*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown       Test Teardown And Restore Guest Clock


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Keywords ***
Login Remote User Through CLI
    Log In
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window


Test Teardown And Restore Guest Clock
    IF    ${guest_clock_changed}
        Run Keyword And Continue On Failure    Restore Guest Clock
    END
    utils.Test Teardown


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

    Login Remote User Through CLI

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


Test fresh GDM login falls back to local password when token verification fails due to network issues
    [Documentation]    Verify that a registered user can start a fresh GDM login with their
    ...    local password when optional provider token verification fails.

    Login Remote User Through CLI
    Log Out

    Change Broker Configuration    force_access_check_with_provider    false
    Block Network Access To Identity Provider

    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}


Test locked session unlock falls back to local password when token verification fails due to network issues
    [Documentation]    Verify that a registered user can unlock a locked session with their
    ...    local password when optional provider token verification fails.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}

    Change Broker Configuration    force_access_check_with_provider    false
    Block Network Access To Identity Provider

    Lock Screen
    Unlock Screen With Password    ${local_password}


Test fresh GDM login falls back to local password when token verification fails due to clock skew
    [Documentation]    Verify that a registered user can start a fresh GDM login with their
    ...    local password when optional provider token verification fails.

    Login Remote User Through CLI
    Log Out

    Change Broker Configuration    force_access_check_with_provider    false
    Set Guest Clock In Future
    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}


Test locked session unlock falls back to local password when token verification fails due to clock skew
    [Documentation]    Verify that a registered user can unlock a locked session with their
    ...    local password when optional provider token verification fails.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}

    Change Broker Configuration    force_access_check_with_provider    false
    Set Guest Clock In Future

    Lock Screen
    Unlock Screen With Password    ${local_password}
