*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    # entra_password is an Entra ID-specific broker option; other brokers
    # (e.g. google) don't offer it, so there is nothing to test there.
    Skip If    '${BROKER_SNAP_NAME}' != 'authd-msentraid'
    ...    entra_password is only supported by the msentraid broker
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Enable the Entra ID password flow and disable device auth so only the
    # new password+MFA mode is offered, avoiding a provider-selection menu.
    # entra_password requires register_device=true (or a client_secret) to fetch
    # groups from Microsoft Graph on first login.
    Change Broker Configuration
    ...    register_device=true
    ...    entra_password=true
    ...    device_auth=false


*** Variables ***
${username}        %{E2E_USER}
# Check If User Was Added Properly uses this cached local password when it
# verifies that sudo prompts for, and accepts, the post-login local password.
${local_password}    %{E2E_PASSWORD}


*** Test Cases ***
Test login with CLI using Entra ID password and MFA
    [Documentation]    Verify that a user can authenticate via the Entra ID direct-password
    ...    + MFA flow through the CLI (machinectl login).
    ...
    ...    With device_auth disabled the broker auto-selects the single available
    ...    authentication mode (entra_password), so the user goes straight to the
    ...    password prompt after choosing the provider. After successful MFA the
    ...    Entra password is cached locally; the provisioning checks verify that
    ...    the cached password works for sudo.

    # Log in with local user (brings up the desktop so we can open a terminal).
    Log In

    # First login: Entra ID password + TOTP MFA.
    Open Terminal
    Log In With Remote User Through CLI: Entra Password    ${username}
    # This shared provisioning check covers NSS, group membership, and the
    # cached local-password path via sudo.
    Check If User Was Added Properly    ${username}

    # Verify the user was provisioned in the system.  NSS may be briefly
    # unavailable while authd commits the new user record, so retry.
    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}
