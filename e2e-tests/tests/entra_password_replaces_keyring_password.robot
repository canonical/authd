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


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${keyring_secret}    s3cr3t-survives-entra


*** Test Cases ***
Replacing local password with Entra password re-keys the keyring
    [Documentation]    After a user replaces their local password with their Entra ID
    ...    password using passwd, the GNOME login keyring must be re-keyed to the
    ...    Entra password and remain unlockable on the next GDM login via the Entra
    ...    ID password flow.
    ...
    ...    Regression guard: pam_authd sets PAM_OLDAUTHTOK (old local password) and
    ...    PAM_AUTHTOK (new Entra password) during the passwd change so
    ...    pam_gnome_keyring re-keys the existing keyring. On the next GDM login
    ...    with the Entra ID password, pam_authd sets PAM_AUTHTOK to the Entra
    ...    password so pam_gnome_keyring can unlock the re-keyed keyring and any
    ...    previously stored secret is preserved.

    # Log in with device authentication. This creates the login keyring and
    # unlocks it with ${local_password}.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check that GNOME keyring is unlocked
    Store Secret In GNOME Keyring    ${keyring_secret}

    # Replace the local password with the Entra ID password. pam_authd sets
    # PAM_OLDAUTHTOK to the old local password and PAM_AUTHTOK to the Entra
    # password, so pam_gnome_keyring re-keys the existing keyring to the Entra
    # password.
    Open Terminal
    Change Password    ${local_password}    %{E2E_PASSWORD}
    Close Focused Window
    Log Out

    # Enable the Entra ID password flow and disable device auth so the next
    # GDM login goes through the Entra password + MFA path.
    Change Broker Configuration
    ...    entra_password=true
    ...    device_auth=false
    ...    register_device=true

    # Log in with the Entra ID password via GDM. pam_authd sets PAM_AUTHTOK to
    # the Entra password, which matches the re-keyed keyring password, so
    # pam_gnome_keyring can unlock the keyring and the stored secret is still
    # accessible.
    Log In With Remote User Through GDM: Entra Password    ${username}
    Check that GNOME keyring is unlocked
    Check Secret In GNOME Keyring    ${keyring_secret}
