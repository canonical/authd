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
    [Documentation]    Verify that a registered user can still log in with their local
    ...    password when ``force_access_check_with_provider`` is enabled and the
    ...    identity provider is reachable.
    ...
    ...    By default a local-password login checks with the provider when it is
    ...    reachable and can use the cached token when it is not. The option prevents
    ...    that offline fallback; it does not add an online revocation check. This test
    ...    covers the online case, where the provider is reachable and the normal check
    ...    succeeds.
    ...
    ...    The user is registered with the device code flow before the option is
    ...    enabled, so registration happens while the provider is reachable and before
    ...    the offline restriction is tested.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    Change Broker Configuration    force_access_check_with_provider    true

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}


Test second login fails with force_access_check_with_provider enabled offline
    [Documentation]    Verify that a registered user cannot log in with their local
    ...    password when ``force_access_check_with_provider`` is enabled and the
    ...    identity provider is unreachable.
    ...
    ...    This is the offline half of the option. Outbound HTTPS is blocked with
    ...    iptables to simulate an unreachable provider, so the forced token refresh
    ...    cannot be made. The broker must refuse to fall back to the cached token and
    ...    leave the user with no usable authentication mode. This is the documented
    ...    trade-off of the option: it keeps revoked users out at the cost of blocking
    ...    logins during a network outage.
    ...
    ...    The iptables rules are not cleaned up here; the VM snapshot is restored at
    ...    the start of the next test.

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
