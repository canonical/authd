*** Settings ***
Resource         resources/utils.resource
Resource         resources/authd.resource
Resource         resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup       utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown    Test Teardown And Restore Guest Clock


*** Variables ***
${username}          %{E2E_USER}
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
    Login Remote User Through CLI

    Change Broker Configuration    force_access_check_with_provider    true

    # Block outbound HTTPS to simulate the identity provider being unreachable.
    Block Network Access To Identity Provider

    Open Terminal
    Try Log In With Remote User    ${username}
    Check That Remote User Has No Available Authentication Modes

Test local password login falls back when refresh fails after session starts
    [Documentation]    Verify that optional provider checks use the cached password when
    ...    refresh fails after the broker session has started online.

    Login Remote User Through CLI

    Change Broker Configuration    force_access_check_with_provider    false
    Open Terminal
    Start Remote User Local Password Login    ${username}
    Block Network Access To Identity Provider
    Submit Remote User Local Password    ${local_password}
    Match Text    ${username.lower()}@ubuntu:~$    120
    Wait Until Keyword Succeeds    30s    1s    Check Refresh Failure Was Logged    ${username}

Test local password login is denied when forced refresh fails after session starts
    [Documentation]    Verify that forced provider checks deny local password login when
    ...    refresh fails after the broker session has started online.

    Login Remote User Through CLI

    Change Broker Configuration    force_access_check_with_provider    true
    Open Terminal
    Start Remote User Local Password Login    ${username}
    Block Network Access To Identity Provider
    Submit Remote User Local Password    ${local_password}
    Match Text    Failed to refresh token    120
    Match Text    @ubuntu:~$    120

Test fresh GDM login falls back when refresh fails after session starts
    [Documentation]    Verify that optional provider checks use the cached password when
    ...    refresh fails after a GDM authentication flow has started online.
    ...
    ...    The test waits for GDM to show the local-password prompt before blocking
    ...    the network. A broker log entry confirms that token refresh was attempted.

    Login Remote User Through CLI
    Log Out

    Change Broker Configuration    force_access_check_with_provider    false
    Start Log In With Remote User Through GDM: Local Password    ${username}
    Block Network Access To Identity Provider
    Continue Log In With Remote User Through GDM: Local Password    ${local_password}
    Wait Until Keyword Succeeds    30s    1s    Check Refresh Failure Was Logged    ${username}

Test fresh GDM login falls back when the provider is unavailable before authentication starts
    [Documentation]    Verify that a registered user can use the cached password when the
    ...    provider is unavailable before the GDM authentication flow starts.
    ...
    ...    This covers provider discovery and offline session setup, not a token refresh
    ...    failure during authentication.

    Login Remote User Through CLI
    Log Out

    Change Broker Configuration    force_access_check_with_provider    false
    Block Network Access To Identity Provider

    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}

Test locked session unlock falls back when refresh fails after authentication starts
    [Documentation]    Verify that optional provider checks use the cached password when
    ...    refresh fails after an unlock authentication flow has started online.
    ...
    ...    The test waits for the unlock password prompt before blocking the network.
    ...    A broker log entry confirms that token refresh was attempted.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Change Broker Configuration    force_access_check_with_provider    false
    Lock Screen
    Start Unlock Screen With Password
    Block Network Access To Identity Provider
    Continue Unlock Screen With Password    ${local_password}
    Wait Until Keyword Succeeds    30s    1s    Check Refresh Failure Was Logged    ${username}

Test locked session unlock falls back when the provider is unavailable before authentication starts
    [Documentation]    Verify that a registered user can use the cached password when the
    ...    provider is unavailable before the unlock authentication flow starts.
    ...
    ...    This covers offline unlock behavior, not a token refresh failure during
    ...    authentication.

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

Start Remote User Local Password Login
    [Arguments]    ${username}
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Hid.Type String    su ${username}
    Hid.Keys Combo    Return
    Match Text    Enter your password:    120

Submit Remote User Local Password
    [Arguments]    ${password}
    Hid.Type String    ${password}
    Hid.Keys Combo    Return

Check Refresh Failure Was Logged
    [Arguments]    ${username}
    ${output} =    SSH.Execute
    ...    sudo journalctl --no-pager --since '10 minutes ago' | grep -F 'Token refresh failed for user "${username}", skipping token refresh'
    Should Not Be Empty    ${output}
