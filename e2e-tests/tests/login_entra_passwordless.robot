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
    Prepare Passwordless Test    E2E_PASSWORDLESS_USER    E2E_PASSWORDLESS_USER is not set; skipping the passwordless Entra test

Test Teardown
    utils.Test Teardown


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
    Retry Passwordless Login    Run Passwordless Login Attempt    Reset Passwordless Test State
    ...    Log In With Remote User Through CLI: Entra Passwordless TAP

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
