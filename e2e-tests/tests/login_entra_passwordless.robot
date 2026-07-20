*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Enable the Entra auth flow with device registration so group membership
    # can be resolved from Microsoft Graph on first login.
    # Disable the device code flow so only entra_auth is offered and the broker
    # auto-selects it, bypassing the provider-selection menu.
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false

Test Teardown
    # Best-effort: removes a leftover TAP so it can't affect other Entra
    # tests sharing ${username}. Retries a few times since this is a network
    # call and a transient failure here would otherwise leave the TAP active
    # for its full lifetime; warnings don't block the VM restore below.
    Run Keyword And Warn On Failure    Wait Until Keyword Succeeds    3x    2s
    ...    EntraTAP.Delete TAP For User    ${username}
    utils.Test Teardown


*** Variables ***
${username}        %{E2E_USER}
# local_password is the password the user sets at the newpassword step.
# It becomes the credential for subsequent offline/local-password logins.
${local_password}    qwer1234


*** Test Cases ***
Test login with CLI using Entra passwordless auth and TAP
    [Documentation]    Verify that an Entra ID user can log in passwordlessly via
    ...    a Temporary Access Pass (TAP) through the CLI (machinectl login).
    ...
    ...    Reuses ``E2E_USER`` instead of a dedicated passwordless account: a TAP
    ...    is minted just before the test and deleted again in teardown, so it
    ...    never lingers and gets picked up by the password-based Entra tests
    ...    that share the account.
    ...
    ...    The broker's passwordless probe finds the TAP and returns a code-entry
    ...    MFA challenge instead of a password prompt. With no Entra password
    ...    submitted, the broker chains into the newpassword step to set a local
    ...    password for offline authentication.
    ...
    ...    See ``resources/EntraTAP.py`` for the required tenant policy and Graph
    ...    permissions.

    # Mint a fresh TAP right before use; the teardown removes it again as a backstop.
    ${tap_code} =    EntraTAP.Create TAP For User    ${username}

    # Log in with the local desktop user to open a terminal.
    Log In

    Open Terminal
    Log In With Remote User Through CLI: Entra Passwordless TAP
    ...    ${username}    ${local_password}    ${tap_code}

    # Verify the user was provisioned correctly: NSS visibility, group
    # membership, and that the cached local password works for sudo.
    Check If User Was Added Properly    ${username}

    # NSS may be briefly unavailable while authd commits the new user record.
    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}

    Log Out From Terminal Session
    Close Focused Window

    # Verify that subsequent logins use the cached local password (offline path).
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window
