*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    # The dedicated passwordless account keeps the TAP away from the shared
    # E2E_USER account; a TAP would change the prompt its tests expect.
    ${passwordless_user} =    Get Environment Variable    E2E_PASSWORDLESS_USER    ${EMPTY}
    Set Suite Variable    ${username}    ${passwordless_user}
    utils.Test Setup    snapshot=%{BROKER}-installed
    IF    not $username
        Skip    E2E_PASSWORDLESS_USER is not set; skipping the passwordless Entra test
    END
    Configure Passwordless Broker

Test Teardown
    utils.Test Teardown

Configure Passwordless Broker
    # Enable the Entra auth flow with device registration so group membership
    # can be resolved from Microsoft Graph on first login.
    # Disable the device code flow so only entra_auth is offered and the broker
    # auto-selects it, bypassing the auth-mode selection menu.
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false

Reset Passwordless Test State
    utils.Restore Snapshot    %{BROKER}-installed
    Configure Passwordless Broker

Run Passwordless Login Attempt
    VAR    ${tap_id}    ${None}
    TRY
        # A concurrent release variant may hold the account's one TAP. Wait
        # for it to finish before creating this attempt's TAP.
        ${tap_code}    ${tap_id} =    Wait Until Keyword Succeeds    12x    60s
        ...    EntraTAP.Create TAP For User    ${username}

        Log In
        Open Terminal
        Log In With Remote User Through CLI: Entra Passwordless TAP
        ...    ${username}    ${local_password}    ${tap_code}
    FINALLY
        IF    $tap_id is not None
            Run Keyword And Warn On Failure    Wait Until Keyword Succeeds    3x    2s
            ...    EntraTAP.Delete Tap By Id    ${username}    ${tap_id}
        END
    END


*** Variables ***
${username}        ${EMPTY}
# local_password is the password the user sets at the newpassword step.
# It becomes the credential for subsequent offline/local-password logins.
${local_password}    qwer1234


*** Test Cases ***
Test login with CLI using Entra passwordless auth and TAP
    [Documentation]    Verify that an Entra ID user can log in passwordlessly via
    ...    a Temporary Access Pass (TAP) through the CLI (machinectl login).
    ...
    ...    Uses the dedicated ``E2E_PASSWORDLESS_USER`` account, so its TAP
    ...    cannot change the prompt shown to password-based Entra tests.
    ...
    ...    The broker's passwordless probe finds the TAP and returns a code-entry
    ...    MFA challenge instead of a password prompt. With no Entra password
    ...    submitted, the broker chains into the newpassword step to set a local
    ...    password for offline authentication.
    ...
    ...    See ``resources/EntraTAP.py`` for the required tenant policy and Graph
    ...    permissions.

    # TAP creation is not an atomic lock. If a concurrent release variant
    # replaces this attempt's TAP after it is minted, reset the VM and retry
    # the complete TAP login instead of reusing the broken terminal session.
    FOR    ${attempt}    IN RANGE    3
        ${status}    ${message} =    Run Keyword And Ignore Error
        ...    Run Passwordless Login Attempt
        IF    '${status}' == 'PASS'    BREAK
        IF    ${attempt} < 2
            Reset Passwordless Test State
        END
    END
    Should Be Equal    ${status}    PASS    msg=${message}

    # Verify the user was provisioned correctly: NSS visibility, group
    # membership, and that the cached local password works for sudo.
    Check If User Was Added Properly    ${username}

    # NSS may be briefly unavailable while authd commits the new user record.
    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}

    Log Out From Terminal Session
    Close Focused Window

    # Verify the cached password through the offline path. With network access
    # enabled, authd refreshes the TAP-issued token before checking the local
    # password, which is outside this assertion.
    Block Network Access To Identity Provider
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window
