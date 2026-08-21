*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Enable the Entra ID password flow and disable device auth so only the
    # password+MFA mode is offered, avoiding a provider-selection menu.
    # entra_password requires register_device=true (or a client_secret) to fetch
    # groups from Microsoft Graph on first login. It stays enabled throughout,
    # alongside the local password cached by the first login, so polkit can
    # later offer a choice between the two.
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_password    true
    Change Broker Configuration    device_code    false


*** Variables ***
${username}        %{E2E_USER}
# After the first successful Entra ID password + MFA login, authd caches the
# Entra password locally; that same password is what the first polkit prompt
# below expects.
${local_password}    %{E2E_PASSWORD}


*** Test Cases ***
Test GDM login with Entra ID password and MFA followed by polkit authentication
    [Documentation]    Verify that a user can log in via GDM using the direct
    ...    Entra ID password + MFA flow, and that polkit then authenticates them
    ...    twice: once with the password cached by that login, and once by
    ...    switching back to a fresh Entra ID password + MFA authentication.
    ...
    ...    The Entra ID user is also added to the sudo group so that polkit
    ...    authenticates them as themselves rather than falling back to the
    ...    local admin (ubuntu) user.

    Log In With Remote User Through GDM: Entra Password    ${username}
    Check If User Was Added Properly    ${username}

    # Add the Entra ID user to the sudo group so that polkit authenticates them
    # as themselves rather than falling back to the local admin (ubuntu) user.
    SSH.Execute    sudo usermod -aG sudo ${username}

    Open Terminal

    # Run pkexec to create a marker file as root; this triggers the polkit agent.
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-cached
    Hid.Keys Combo    Return

    # The GNOME polkit agent pops up a dialog; since the Entra ID user is in the
    # sudo group, polkit authenticates them as themselves through PAM/authd,
    # using the password cached by the GDM login above.
    Match Text    Enter your password    60
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Verify polkit granted access: the marker file must now exist as root-owned.
    Wait Until Keyword Succeeds    5s    1s
    ...    SSH.Execute    test -f /tmp/polkit-authd-test-cached

    # Trigger polkit again creating a different marker file
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-entra
    Hid.Keys Combo    Return

    # Polkit auto-selects authentication via local password.
    # Cancel it ('r') to go back to the authentication-flow menu and choose the
    # Entra ID password + MFA flow instead.
    Match Text    Enter your password    60
    Hid.Type String    r
    Hid.Keys Combo    Return

    Match Text    Choose your authentication flow    120
    Match Text    2. Entra ID password + MFA    15
    Hid.Type String    2
    Hid.Keys Combo    Return

    Match Text    Enter your Entra ID password:    30    similarity=90
    Hid.Type String    %{E2E_PASSWORD}
    Hid.Keys Combo    Return

    Match Text    Enter your MFA code:    120    similarity=88
    ${totp_code} =    TOTP.Generate Totp Code
    Hid.Type String    ${totp_code}
    Hid.Keys Combo    Return

    # Verify polkit granted access checking if new marker file exists as root.
    Wait Until Keyword Succeeds    5s    1s
    ...    SSH.Execute    test -f /tmp/polkit-authd-test-entra

    Log Out
