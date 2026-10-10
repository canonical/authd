*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure
Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Enable the Entra auth flow and disable device auth so only the
    # new password+MFA mode is offered, avoiding a provider-selection menu.
    # entra_auth requires register_device=true (or a client_secret) to fetch
    # groups from Microsoft Graph on first login.
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false


*** Variables ***
${username}        %{E2E_USER}
# Check If User Was Added Properly uses this cached local password when it
# verifies that sudo prompts for, and accepts, the post-login local password.
${local_password}    %{E2E_PASSWORD}


*** Test Cases ***
Test login with CLI using Entra auth and MFA
    [Documentation]    Verify that a user can authenticate via the Entra ID direct-password
    ...    + MFA flow through the CLI (machinectl login).
    ...
    ...    With the device code flow disabled the broker auto-selects the single available
    ...    authentication mode (entra_auth), so the user goes straight to the
    ...    password prompt after choosing the provider. After successful MFA the
    ...    Entra password is cached locally; the provisioning checks verify that
    ...    the cached password works for sudo.

    # Log in with local user (brings up the desktop so we can open a terminal).
    Log In

    # First login: Entra ID password + TOTP MFA.
    Open Terminal
    Log In With Remote User Through CLI: Entra Auth    ${username}
    # This shared provisioning check covers NSS, group membership, and the
    # cached local-password path via sudo.
    Check If User Was Added Properly    ${username}

    # Verify the user was provisioned in the system.  NSS may be briefly
    # unavailable while authd commits the new user record, so retry.
    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}

Test GDM login with Entra ID password and MFA
    [Documentation]    Verify that a user can log in through GDM with the direct Entra
    ...    ID password + MFA flow.
    ...
    ...    This is the GDM counterpart of the CLI test above and shares its setup: with
    ...    the device code flow disabled the broker auto-selects entra_auth, so after
    ...    picking the broker the greeter goes straight to the Entra ID password prompt
    ...    and then the MFA code prompt.
    ...
    ...    This flow sets no separate local password; the Entra password is cached
    ...    instead, so the provisioning check uses E2E_PASSWORD when it verifies sudo.

    Log In With Remote User Through GDM: Entra Password    ${username}
    Check If User Was Added Properly    ${username}

    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}
